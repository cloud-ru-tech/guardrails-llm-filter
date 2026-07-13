package sensitive

import (
	"cmp"
	"fmt"
	"log/slog"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/registry"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/validation"
)

// Match is one regex hit in source text.
type Match struct {
	RuleID      string // stable rule ID (Rule.ID)
	DataType    int    // parent data_type from rule config
	Start       int    // byte offset in original text
	End         int    // byte offset in original text (exclusive)
	FullText    string // semantic sensitive value selected for replacement
	Placeholder string // placeholder name selected for replacement
}

// Scan finds sensitive values in request text for the given data types. It
// resolves the compiled rules for ruleIDs itself; callers that scan many texts
// against a fixed rule set should resolve once and use ScanRules instead.
func (s *Scanner) Scan(text string, ruleIDs []string) ([]Match, error) {
	return s.ScanRules(text, s.registry.GetCompiledRulesByRuleIDs(ruleIDs))
}

// ScanRules finds sensitive values in text using a pre-resolved compiled-rule
// set. The rules slice is never mutated, so the same slice may be reused across
// many texts (the keyword pre-filter allocates its own filtered view).
func (s *Scanner) ScanRules(text string, rules []registry.CompiledRule) ([]Match, error) {
	if len(rules) == 0 {
		return nil, nil
	}

	if s.keywordPrefilter {
		rules = filterByKeywords(text, rules)
		if len(rules) == 0 {
			return nil, nil
		}
	}

	matches, err := scanRules(text, rules)
	if err != nil {
		return nil, err
	}
	return resolveConflicts(text, matches), nil
}

// parallelTextThreshold / parallelRuleThreshold gate the goroutine fan-out.
// On the hot BUFFERED request path most fields are tiny (a handful of bytes),
// where spawning up to NumCPU workers to run a few anchored regexes costs far
// more than running them sequentially. Mirror the placeholder scanner: fan out
// only when the text is large and the rule set is big enough to split.
const (
	parallelTextThreshold = 4 * 1024
	parallelRuleThreshold = 4
)

func scanRules(text string, rules []registry.CompiledRule) ([]Match, error) {
	if len(text) < parallelTextThreshold || len(rules) <= parallelRuleThreshold {
		// Sequential path runs in the caller's goroutine, so a panic here is
		// covered by the gRPC recovery interceptor (or the mask layer's own
		// recover). Only the fan-out below needs its own guard.
		var all []Match
		for _, cr := range rules {
			all = append(all, scanRule(text, cr)...)
		}
		return all, nil
	}
	return scanParallel(text, rules)
}

// filterByKeywords drops rules whose pre-filter keywords are all absent from the
// text, so the caller skips their regex entirely. Only rules the registry marked
// pre-filter-eligible (their regex guarantees a keyword in every match, exposed
// as CompiledRule.PrefilterKeywords) participate; every other rule is always
// kept, so the pre-filter is recall-preserving. PrefilterKeywords are already
// lowercased at compile time.
//
// rules may be shared across many texts (see ScanRules), so this returns a
// freshly allocated filtered view and never mutates the input.
func filterByKeywords(text string, rules []registry.CompiledRule) []registry.CompiledRule {
	// Lowercase the body lazily: only the first prefilter-eligible rule forces
	// it, so a rule set with no eligible rules pays nothing (and skips the whole
	// body copy). Once computed it is reused for the rest of the pass.
	var lower string
	lowered := false

	out := make([]registry.CompiledRule, 0, len(rules))
	for _, cr := range rules {
		if len(cr.PrefilterKeywords) == 0 {
			out = append(out, cr) // ineligible rules are always scanned
			continue
		}
		if !lowered {
			lower = strings.ToLower(text)
			lowered = true
		}
		if containsAnyKeyword(lower, cr.PrefilterKeywords) {
			out = append(out, cr)
		}
	}
	return out
}

func containsAnyKeyword(lowerText string, loweredKeywords []string) bool {
	for _, kw := range loweredKeywords {
		if strings.Contains(lowerText, kw) {
			return true
		}
	}
	return false
}

func scanParallel(text string, rules []registry.CompiledRule) ([]Match, error) {
	buckets := bucketRules(rules)
	if len(buckets) == 0 {
		return nil, nil
	}

	results := make([][]Match, len(buckets))
	errs := make([]error, len(buckets))
	var wg sync.WaitGroup
	wg.Add(len(buckets))

	for i, bucket := range buckets {
		go func(worker int, b []registry.CompiledRule) {
			defer wg.Done()
			// The gRPC recovery interceptor only covers the handler goroutine,
			// not children it spawns. Without this guard a panic in scanRule /
			// validation.Validate would crash the whole process (resetting every
			// in-flight stream on the replica); convert it into an error so the
			// caller degrades fail-open instead — the documented contract.
			defer func() {
				if r := recover(); r != nil {
					errs[worker] = fmt.Errorf("sensitive scan worker panic: %v", r)
					slog.Error("sensitive scan worker panic recovered", "panic", r)
				}
			}()
			var matches []Match
			for _, cr := range b {
				matches = append(matches, scanRule(text, cr)...)
			}
			results[worker] = matches
		}(i, bucket)
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}

	var all []Match
	for _, r := range results {
		all = append(all, r...)
	}
	return all, nil
}

func bucketRules(rules []registry.CompiledRule) [][]registry.CompiledRule {
	numWorkers := min(len(rules), runtime.NumCPU())
	if numWorkers == 0 {
		return nil
	}

	buckets := make([][]registry.CompiledRule, numWorkers)
	for i, cr := range rules {
		buckets[i%numWorkers] = append(buckets[i%numWorkers], cr)
	}
	return buckets
}

func scanRule(text string, cr registry.CompiledRule) []Match {
	locs := cr.Re.FindAllStringSubmatchIndex(text, -1)
	if locs == nil {
		return nil
	}

	var matches []Match
	for _, loc := range locs {
		m := buildMatch(text, loc, cr)

		if m.FullText == "" {
			continue
		}
		if cr.MinLength > 0 && len(m.FullText) < cr.MinLength {
			continue
		}
		if !validation.Validate(m.FullText, cr.Rule) {
			continue
		}

		matches = append(matches, m)
	}
	return matches
}

// buildMatch converts raw regexp indexes into the semantic sensitive span that
// should be replaced by the masking layer.
func buildMatch(text string, loc []int, cr registry.CompiledRule) Match {
	start, end, ok := sensitiveSpan(text, loc, cr)
	if !ok {
		return Match{}
	}

	return Match{
		RuleID:      cr.ID,
		DataType:    cr.DataType,
		Start:       start,
		End:         end,
		FullText:    text[start:end],
		Placeholder: cr.Masking.Placeholder,
	}
}

func sensitiveSpan(text string, loc []int, cr registry.CompiledRule) (start int, end int, ok bool) {
	if len(loc) < 2 || loc[0] < 0 || loc[1] < loc[0] || loc[1] > len(text) {
		return 0, 0, false
	}

	if len(cr.Masking.CaptureGroups) == 0 {
		start, end = loc[0], loc[1]
		return start, end, start < end
	}

	for _, group := range cr.Masking.CaptureGroups {
		// regexp submatch indexes are stored as pairs: full match at 0/1,
		// capture group N at 2*N/2*N+1.
		groupIdx := group * 2
		if groupIdx+1 >= len(loc) {
			continue
		}
		groupStart, groupEnd := loc[groupIdx], loc[groupIdx+1]
		if groupStart < 0 || groupEnd <= groupStart || groupEnd > len(text) {
			continue
		}
		return groupStart, groupEnd, true
	}

	return 0, 0, false
}

// resolveConflicts coalesces overlapping matches into a single union span so no
// detected sensitive byte is ever emitted unmasked. Two matches that overlap
// (even partially) are merged into one span [minStart, maxEnd); the merged
// span's original value is text[minStart:maxEnd] and it is attributed to its
// longest constituent so that constituent's placeholder type labels the value.
// Non-overlapping matches pass through unchanged. The result is sorted by Start
// ascending and is non-overlapping, as maskText requires.
//
// Masking the union — rather than dropping the shorter of two overlapping
// matches — is deliberate: dropping a match (whichever end wins) would emit the
// dropped match's non-overlapping bytes verbatim, leaking part of a detected
// secret to the upstream LLM.
func resolveConflicts(text string, matches []Match) []Match {
	if len(matches) <= 1 {
		return matches
	}

	slices.SortFunc(matches, func(a, b Match) int {
		if a.Start != b.Start {
			return cmp.Compare(a.Start, b.Start)
		}
		// Longest first at a given start, then RuleID for determinism.
		if leftLen, rightLen := a.End-a.Start, b.End-b.Start; leftLen != rightLen {
			return cmp.Compare(rightLen, leftLen)
		}
		return cmp.Compare(a.RuleID, b.RuleID)
	})

	resolved := make([]Match, 0, len(matches))

	// Sweep the start-sorted matches, growing one coalesced run until a gap.
	runStart, runEnd := matches[0].Start, matches[0].End
	run := []Match{matches[0]}
	for _, m := range matches[1:] {
		if m.Start < runEnd {
			// Overlaps the current run: absorb it and extend the run's end.
			run = append(run, m)
			if m.End > runEnd {
				runEnd = m.End
			}
			continue
		}
		// Disjoint from the current run: emit it and start a fresh run.
		resolved = append(resolved, coalesce(text, runStart, runEnd, run))
		runStart, runEnd = m.Start, m.End
		run = []Match{m}
	}
	resolved = append(resolved, coalesce(text, runStart, runEnd, run))

	return resolved
}

// coalesce returns the single Match representing a run of overlapping matches.
// A one-element run is returned verbatim, preserving its exact
// FullText/Placeholder/RuleID (the common no-overlap and fully-nested cases —
// no behavior change). A multi-element run becomes one union span [start, end)
// whose original is text[start:end], attributed to the longest constituent.
func coalesce(text string, start, end int, run []Match) Match {
	if len(run) == 1 {
		return run[0]
	}
	primary := run[0]
	for _, m := range run[1:] {
		if preferAsPrimary(m, primary) {
			primary = m
		}
	}
	return Match{
		RuleID:      primary.RuleID,
		DataType:    primary.DataType,
		Start:       start,
		End:         end,
		FullText:    text[start:end],
		Placeholder: primary.Placeholder,
	}
}

// preferAsPrimary reports whether m should represent a merged span in place of
// cur: longest wins, then lowest Start, then RuleID (deterministic).
func preferAsPrimary(m, cur Match) bool {
	if ml, cl := m.End-m.Start, cur.End-cur.Start; ml != cl {
		return ml > cl
	}
	if m.Start != cur.Start {
		return m.Start < cur.Start
	}
	return m.RuleID < cur.RuleID
}
