package placeholder

import (
	"cmp"
	"fmt"
	"log/slog"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/placeholderfmt"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/registry"
)

// Match is one regex hit in source text.
type Match struct {
	RuleID      string // stable rule ID (Rule.ID)
	Start       int    // byte offset in original text
	End         int    // byte offset in original text (exclusive)
	Placeholder string // canonical synthetic placeholder, e.g. <EMAIL_1>
}

// Scan finds placeholder matches for the given rule IDs. Callers that scan
// many texts against a fixed rule set (e.g. per-chunk SSE demasking) should
// call ResolveRules once and ScanRules per text instead.
func (s *Scanner) Scan(text string, ruleIDs []string) ([]Match, error) {
	return s.ScanRules(text, s.ResolveRules(ruleIDs))
}

// ResolveRules resolves rule IDs to compiled rules from the current registry
// snapshot. The returned slice stays valid for the caller's lifetime: rules
// are immutable snapshots, so binding them once per stream is correct even
// while the registry is hot-swapped underneath.
func (s *Scanner) ResolveRules(ruleIDs []string) []registry.CompiledRule {
	return s.registry.GetCompiledRulesByRuleIDs(ruleIDs)
}

// ScanRules finds placeholder matches using an already-resolved rule set.
func (s *Scanner) ScanRules(text string, rules []registry.CompiledRule) ([]Match, error) {
	if len(rules) == 0 {
		return nil, nil
	}
	matches, err := scanRules(text, rules)
	if err != nil {
		return nil, err
	}
	return resolveConflicts(matches), nil
}

// parallelTextThreshold gates the goroutine fan-out: for the token-sized
// fragments of the SSE demask path, spawning workers costs far more than
// running a handful of anchored regexes sequentially. Fan out only when the
// text is large and the rule set is big enough to split meaningfully.
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
			// not children it spawns. Without this guard a panic in scanRule
			// would crash the whole process (resetting every in-flight stream);
			// convert it into an error so the caller degrades fail-open instead.
			defer func() {
				if r := recover(); r != nil {
					errs[worker] = fmt.Errorf("placeholder scan worker panic: %v", r)
					slog.Error("placeholder scan worker panic recovered", "panic", r)
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
	for _, matches := range results {
		all = append(all, matches...)
	}
	return all, nil
}

func scanRule(text string, cr registry.CompiledRule) []Match {
	if cr.PlaceholderRe == nil || strings.TrimSpace(cr.Masking.Placeholder) == "" {
		return nil
	}

	locs := cr.PlaceholderRe.FindAllStringSubmatchIndex(text, -1)
	if len(locs) == 0 {
		return nil
	}

	matches := make([]Match, 0, len(locs))
	for _, loc := range locs {
		if len(loc) < 4 {
			continue
		}
		indexStart, indexEnd := loc[2], loc[3]
		if indexStart < 0 || indexEnd < 0 || indexStart >= indexEnd {
			continue
		}

		index, err := strconv.Atoi(text[indexStart:indexEnd])
		if err != nil || index <= 0 {
			continue
		}

		matches = append(matches, Match{
			RuleID:      cr.ID,
			Start:       loc[0],
			End:         loc[1],
			Placeholder: placeholderfmt.Format(cr.Masking.Placeholder, index),
		})
	}
	return matches
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

func resolveConflicts(matches []Match) []Match {
	if len(matches) <= 1 {
		return matches
	}

	slices.SortFunc(matches, func(a, b Match) int {
		if a.Start != b.Start {
			return cmp.Compare(a.Start, b.Start)
		}
		leftLen := a.End - a.Start
		rightLen := b.End - b.Start
		if leftLen != rightLen {
			return cmp.Compare(rightLen, leftLen)
		}
		return cmp.Compare(a.RuleID, b.RuleID)
	})

	resolved := make([]Match, 0, len(matches))
	maxEnd := -1
	for _, match := range matches {
		if match.Start < maxEnd {
			continue
		}
		resolved = append(resolved, match)
		if match.End > maxEnd {
			maxEnd = match.End
		}
	}
	return resolved
}
