package registry

import (
	"fmt"
	"regexp"
	"regexp/syntax"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/validation"
)

// CompiledRule is a Rule with its pre-compiled *regexp.Regexp and placeholder policy.
type CompiledRule struct {
	rule.Rule
	Re             *regexp.Regexp
	PlaceholderRe  *regexp.Regexp
	PlaceholderLen int

	// PrefilterKeywords holds the rule's keywords, lowercased, but ONLY when the
	// regex guarantees at least one of them appears in every match. Empty when the
	// rule has no keywords or when the pre-filter would be lossy — such rules are
	// always scanned. The sensitive scanner uses this for a recall-preserving
	// keyword pre-filter.
	PrefilterKeywords []string
}

// Registry holds all compiled rules with efficient lookup by data_type and rule_id.
type Registry struct {
	rules      []CompiledRule // single storage, allocated once at startup
	byDataType map[int][]int  // data_type → indices into rules
	byID       map[string]int // rule.ID → index into rules
}

// NewRegistry creates an empty Registry ready to accept rules via Register.
func NewRegistry() *Registry {
	return &Registry{
		rules:      make([]CompiledRule, 0),
		byDataType: make(map[int][]int),
		byID:       make(map[string]int),
	}
}

// Register compiles each rule's regex and adds it to the registry.
// Panics if any regex is invalid — rules are static config and a bad regex
// is a deployment error, not a runtime condition. For runtime-supplied rules
// use Build or Add, which return errors instead.
func (reg *Registry) Register(rules ...rule.Rule) {
	for _, rl := range rules {
		if err := reg.Add(rl); err != nil {
			panic(err)
		}
	}
}

// Build compiles all rules into a new Registry, returning an error for the
// first invalid rule. This is the non-panicking counterpart of
// NewRegistry+Register for rules that arrive at runtime (e.g. via the
// configuration API).
func Build(rules ...rule.Rule) (*Registry, error) {
	reg := NewRegistry()
	for _, rl := range rules {
		if err := reg.Add(rl); err != nil {
			return nil, err
		}
	}
	return reg, nil
}

// Add compiles a single rule and adds it to the registry.
// It returns an error when the rule is invalid or its ID is already taken.
func (reg *Registry) Add(rl rule.Rule) error {
	cr, err := CompileRule(reg, rl)
	if err != nil {
		return err
	}
	idx := len(reg.rules)
	reg.rules = append(reg.rules, cr)
	reg.byDataType[rl.DataType] = append(reg.byDataType[rl.DataType], idx)
	reg.byID[rl.ID] = idx
	return nil
}

// GetRulesByIDs returns the Rule configs for the given rule IDs.
func (reg *Registry) GetRulesByIDs(ruleIDs ...string) []rule.Rule {
	if len(ruleIDs) == 0 {
		return nil
	}
	out := make([]rule.Rule, 0, len(ruleIDs))
	for _, id := range ruleIDs {
		if idx, ok := reg.byID[id]; ok {
			out = append(out, reg.rules[idx].Rule)
		}
	}
	return out
}

// GetRuleIDsByDataTypes returns the sorted rule IDs for the given data types.
func (reg *Registry) GetRuleIDsByDataTypes(dataTypes ...uint32) []string {
	if len(dataTypes) == 0 {
		return nil
	}
	var out []string
	for _, dt := range dataTypes {
		for _, idx := range reg.byDataType[int(dt)] {
			out = append(out, reg.rules[idx].ID)
		}
	}
	sort.Strings(out)
	return out
}

// GetRulesByDataTypes returns the Rule configs for the given data types.
// Use this when callers only need rule configuration, not regex execution.
func (reg *Registry) GetRulesByDataTypes(dataTypes ...uint32) []rule.Rule {
	if len(dataTypes) == 0 {
		return nil
	}
	var out []rule.Rule
	for _, dt := range dataTypes {
		for _, idx := range reg.byDataType[int(dt)] {
			out = append(out, reg.rules[idx].Rule)
		}
	}
	return out
}

// GetMaskingPlaceholderByRuleID returns the placeholder prefix for the given rule ID.
func (reg *Registry) GetMaskingPlaceholderByRuleID(ruleID string) string {
	if idx, ok := reg.byID[ruleID]; ok {
		return reg.rules[idx].Masking.Placeholder
	}
	return ""
}

// GetMaxPlaceholderLenByRuleIDs returns the largest placeholder regex match length.
func (reg *Registry) GetMaxPlaceholderLenByRuleIDs(ruleIDs ...string) int {
	maxLen := 0
	for _, id := range ruleIDs {
		idx, ok := reg.byID[id]
		if !ok {
			continue
		}
		if reg.rules[idx].PlaceholderLen > maxLen {
			maxLen = reg.rules[idx].PlaceholderLen
		}
	}
	return maxLen
}

// HasRulesForDataTypes reports whether any rules are registered for the given data types.
func (reg *Registry) HasRulesForDataTypes(dataTypes []uint32) bool {
	for _, dt := range dataTypes {
		if len(reg.byDataType[int(dt)]) > 0 {
			return true
		}
	}
	return false
}

// GetCompiledRulesByDataTypes returns compiled rules for the given data types and
// whether any rule has keywords (for keyword pre-filter allocation in the scanner).
func (reg *Registry) GetCompiledRulesByDataTypes(dataTypes []uint32) ([]CompiledRule, bool) {
	if len(dataTypes) == 0 {
		return nil, false
	}
	var out []CompiledRule
	hasKeywords := false
	for _, dt := range dataTypes {
		for _, idx := range reg.byDataType[int(dt)] {
			cr := reg.rules[idx]
			if !hasKeywords && len(cr.Keywords) > 0 {
				hasKeywords = true
			}
			out = append(out, cr)
		}
	}
	return out, hasKeywords
}

// PrefilterIneligibleRuleIDs returns the sorted IDs of rules that declare
// keywords but are NOT eligible for the keyword pre-filter (their regex does
// not guarantee a keyword in every match). Such rules are always scanned; the
// list is surfaced at startup so operators can see what the pre-filter skips.
func (reg *Registry) PrefilterIneligibleRuleIDs() []string {
	var out []string
	for _, cr := range reg.rules {
		if len(cr.Keywords) > 0 && len(cr.PrefilterKeywords) == 0 {
			out = append(out, cr.ID)
		}
	}
	sort.Strings(out)
	return out
}

// GetCompiledRulesByRuleIDs returns compiled rules for the given rule IDs.
func (reg *Registry) GetCompiledRulesByRuleIDs(ruleIDs []string) []CompiledRule {
	if len(ruleIDs) == 0 {
		return nil
	}
	out := make([]CompiledRule, 0, len(ruleIDs))
	for _, id := range ruleIDs {
		if idx, ok := reg.byID[id]; ok {
			out = append(out, reg.rules[idx])
		}
	}
	return out
}

// ResolveForDataTypes returns the rule IDs enabled for the given data types
// together with their compiled rules, resolved against this single snapshot.
// Resolving both in one call (rather than GetRuleIDsByDataTypes followed by a
// separate GetCompiledRulesByRuleIDs) guarantees the two never straddle a
// registry Swap, which would otherwise drop a rule from the request mid-reload.
func (reg *Registry) ResolveForDataTypes(dataTypes []uint32) ([]string, []CompiledRule) {
	ruleIDs := reg.GetRuleIDsByDataTypes(dataTypes...)
	return ruleIDs, reg.GetCompiledRulesByRuleIDs(ruleIDs)
}

// CompileRule validates and compiles a single rule against the registry
// (the registry is consulted only for duplicate rule IDs). It is the single
// validation path shared by startup file loading and the configuration API.
func CompileRule(reg *Registry, rl rule.Rule) (CompiledRule, error) {
	if strings.TrimSpace(rl.ID) == "" {
		return CompiledRule{}, fmt.Errorf("compile guardrails rule: empty rule_id for rule %q", rl.Name)
	}
	if _, exists := reg.byID[rl.ID]; exists {
		return CompiledRule{}, fmt.Errorf("compile guardrails rule %q: duplicate rule_id", rl.ID)
	}
	for _, validator := range rl.Validators {
		if !validation.IsKnown(validator) {
			return CompiledRule{}, fmt.Errorf("compile guardrails rule %q: unsupported validator %q", rl.ID, validator)
		}
	}

	re, err := regexp.Compile("(?m)" + rl.Regex)
	if err != nil {
		return CompiledRule{}, fmt.Errorf("compile guardrails rule %q regex: %w", rl.ID, err)
	}
	if err := validateMaskingConfig(rl, re); err != nil {
		return CompiledRule{}, err
	}

	placeholderRe, placeholderLen, err := compilePlaceholderRegex(rl)
	if err != nil {
		return CompiledRule{}, fmt.Errorf("compile guardrails rule %q placeholder regex: %w", rl.ID, err)
	}

	return CompiledRule{
		Rule:              rl,
		Re:                re,
		PlaceholderRe:     placeholderRe,
		PlaceholderLen:    placeholderLen,
		PrefilterKeywords: prefilterKeywords(rl.Regex, rl.Keywords),
	}, nil
}

// prefilterKeywords returns the rule's keywords lowercased, but only when the
// regex guarantees that at least one of them appears in every match — i.e. when
// using them as a pre-filter cannot drop a value the regex would otherwise
// catch. Otherwise it returns nil, so the scanner never pre-filters that rule.
// This keeps the keyword pre-filter recall-preserving.
func prefilterKeywords(regex string, keywords []string) []string {
	if len(keywords) == 0 {
		return nil
	}
	lowered := make([]string, len(keywords))
	for i, kw := range keywords {
		lowered[i] = strings.ToLower(kw)
	}
	// The scanner compiles with a "(?m)" prefix; parse the same source so the
	// syntax tree matches what runs. Flag-only prefixes do not affect literals.
	parsed, err := syntax.Parse(regex, syntax.Perl)
	if err != nil {
		return nil // unparsable here means Compile already failed upstream; be safe
	}
	if !regexGuaranteesKeyword(parsed.Simplify(), lowered) {
		return nil
	}
	return lowered
}

// regexGuaranteesKeyword reports whether every string matched by re is
// guaranteed to contain at least one of loweredKeywords. It is conservative:
// when it cannot prove the guarantee it returns false (the rule is then always
// scanned, never pre-filtered), so a false negative only costs speed, never
// recall.
func regexGuaranteesKeyword(re *syntax.Regexp, loweredKeywords []string) bool {
	switch re.Op {
	case syntax.OpLiteral:
		// The runtime pre-filter lowercases the body with strings.ToLower and
		// compares against the lowercased keyword, but the regex matches with
		// RE2 Unicode case-folding. For a case-insensitive literal those diverge
		// when a rune's fold orbit contains a variant strings.ToLower does not
		// map back to the same form (Greek Σ/σ/ς, long-s ſ↔s). Then RE2 can
		// match text the pre-filter would drop, so refuse the guarantee and
		// always scan the rule (costs speed, never recall).
		if !foldSafeForToLower(re.Rune, re.Flags&syntax.FoldCase != 0) {
			return false
		}
		literal := strings.ToLower(string(re.Rune))
		for _, kw := range loweredKeywords {
			if strings.Contains(literal, kw) {
				return true
			}
		}
		return false

	case syntax.OpConcat:
		// Every child is present in the match, so one guaranteeing child suffices.
		for _, sub := range re.Sub {
			if regexGuaranteesKeyword(sub, loweredKeywords) {
				return true
			}
		}
		return false

	case syntax.OpAlternate:
		// The match takes exactly one branch, so all branches must guarantee it.
		if len(re.Sub) == 0 {
			return false
		}
		for _, sub := range re.Sub {
			if !regexGuaranteesKeyword(sub, loweredKeywords) {
				return false
			}
		}
		return true

	case syntax.OpPlus:
		return regexGuaranteesKeyword(re.Sub[0], loweredKeywords)

	case syntax.OpRepeat:
		if re.Min == 0 {
			return false
		}
		return regexGuaranteesKeyword(re.Sub[0], loweredKeywords)

	case syntax.OpCapture:
		if len(re.Sub) == 0 {
			return false
		}
		return regexGuaranteesKeyword(re.Sub[0], loweredKeywords)

	default:
		// OpStar, OpQuest, OpCharClass, OpAnyChar(NotNL), anchors, OpEmptyMatch,
		// and anything unrecognized cannot guarantee a keyword.
		return false
	}
}

// foldSafeForToLower reports whether pre-filtering on a lowercased literal is
// recall-safe. The runtime lowercases the body with strings.ToLower, so without
// FoldCase the literal matches verbatim and lowercasing both sides always
// agrees. With FoldCase every rune in each rune's Unicode fold orbit must lower
// to the same form as the rune itself; otherwise RE2 can match a variant
// (Greek ς, long-s ſ, ...) whose lowercased body no longer contains the lowered
// keyword, so the pre-filter would drop a real match.
func foldSafeForToLower(runes []rune, foldCase bool) bool {
	if !foldCase {
		return true
	}
	for _, r := range runes {
		lower := unicode.ToLower(r)
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if unicode.ToLower(f) != lower {
				return false
			}
		}
	}
	return true
}

func validateMaskingConfig(rl rule.Rule, re *regexp.Regexp) error {
	for _, group := range rl.Masking.CaptureGroups {
		if group <= 0 {
			return fmt.Errorf("compile guardrails rule %q: capture_groups must contain positive indexes", rl.ID)
		}
		if group > re.NumSubexp() {
			return fmt.Errorf(
				"compile guardrails rule %q: capture group %d exceeds regex capture groups %d",
				rl.ID,
				group,
				re.NumSubexp(),
			)
		}
	}
	return nil
}

func compilePlaceholderRegex(rl rule.Rule) (*regexp.Regexp, int, error) {
	placeholderType := strings.TrimSpace(rl.Masking.Placeholder)
	if placeholderType == "" {
		return nil, 0, nil
	}

	pattern := buildDefaultPlaceholderRegexp(placeholderType)

	re, err := regexp.Compile("(?m)" + pattern)
	if err != nil {
		return nil, 0, err
	}
	if re.NumSubexp() < 1 {
		return nil, 0, fmt.Errorf("placeholder regex must have capture group #1 for placeholder index")
	}

	maxLen := regexpMaxLen(pattern)
	if maxLen == 0 {
		return nil, 0, fmt.Errorf("placeholder regex must be bounded")
	}

	return re, maxLen, nil
}

func buildDefaultPlaceholderRegexp(placeholderType string) string {
	const (
		maxDrift = 3
		indexLen = 9
	)

	tokens := strings.FieldsFunc(placeholderType, func(r rune) bool {
		return r == '_'
	})
	if len(tokens) == 0 {
		tokens = []string{placeholderType}
	}

	sep := fmt.Sprintf(`[\s_-]{0,%d}`, maxDrift)
	var b strings.Builder
	b.WriteString(`(?i)<\s{0,`)
	fmt.Fprintf(&b, "%d", maxDrift)
	b.WriteString("}")

	for i, token := range tokens {
		if i > 0 {
			b.WriteString(sep)
		}
		b.WriteString(regexp.QuoteMeta(token))
	}

	b.WriteString(sep)
	b.WriteString(`([0-9]{1,`)
	fmt.Fprintf(&b, "%d", indexLen)
	b.WriteString(`})\s{0,`)
	fmt.Fprintf(&b, "%d", maxDrift)
	b.WriteString(`}>`)

	return b.String()
}

// maxRuneLenInClass returns the maximum UTF-8 byte length of any rune that
// belongs to the character class described by the alternating [lo, hi] rune
// pairs in runes.
func maxRuneLenInClass(runes []rune) int {
	max := rune(0)
	for i := 0; i+1 < len(runes); i += 2 {
		if runes[i+1] > max {
			max = runes[i+1]
		}
	}
	n := utf8.RuneLen(max)
	if n < 1 {
		return utf8.UTFMax
	}
	return n
}

// regexpMaxLen returns an upper bound on the byte length of any string matched
// by the given RE2 pattern. Returns 0 when the pattern is unbounded or the
// bound cannot be determined.
func regexpMaxLen(pattern string) int {
	parsed, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return 0
	}
	n := syntaxMaxLen(parsed.Simplify())
	if n < 0 {
		return 0
	}
	return n
}

// syntaxMaxLen recursively computes the maximum byte length of strings matched
// by the given syntax tree node.
//
// Return value semantics (internal):
//   - n >= 0: bounded; the match is at most n bytes (0 means zero-width)
//   - n == -1: unbounded / unknown
func syntaxMaxLen(re *syntax.Regexp) int {
	switch re.Op {
	case syntax.OpEmptyMatch,
		syntax.OpBeginLine, syntax.OpEndLine,
		syntax.OpBeginText, syntax.OpEndText,
		syntax.OpWordBoundary, syntax.OpNoWordBoundary:
		return 0

	case syntax.OpLiteral:
		n := 0
		for _, r := range re.Rune {
			n += utf8.RuneLen(r)
		}
		return n

	case syntax.OpCharClass:
		return maxRuneLenInClass(re.Rune)
	case syntax.OpAnyCharNotNL, syntax.OpAnyChar:
		return utf8.UTFMax

	case syntax.OpQuest:
		n := syntaxMaxLen(re.Sub[0])
		if n < 0 {
			return -1
		}
		return n

	case syntax.OpStar, syntax.OpPlus:
		return -1

	case syntax.OpRepeat:
		if re.Max < 0 {
			return -1
		}
		sub := syntaxMaxLen(re.Sub[0])
		if sub < 0 {
			return -1
		}
		return re.Max * sub

	case syntax.OpConcat:
		total := 0
		for _, sub := range re.Sub {
			n := syntaxMaxLen(sub)
			if n < 0 {
				return -1
			}
			total += n
		}
		return total

	case syntax.OpAlternate:
		max := 0
		for _, sub := range re.Sub {
			n := syntaxMaxLen(sub)
			if n < 0 {
				return -1
			}
			if n > max {
				max = n
			}
		}
		return max

	case syntax.OpCapture:
		if len(re.Sub) == 0 {
			return 0
		}
		return syntaxMaxLen(re.Sub[0])

	default:
		return -1
	}
}
