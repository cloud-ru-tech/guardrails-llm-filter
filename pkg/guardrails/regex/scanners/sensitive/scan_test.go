package sensitive_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/registry"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/scanners/sensitive"
)

func TestScannerScan_NoRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		text    string
		ruleIDs []string
		rules   []registry.CompiledRule
	}{
		{
			name:    "empty rule ids",
			text:    "token=secret",
			ruleIDs: nil,
			rules:   nil,
		},
		{
			name:    "unknown rule id",
			text:    "token=secret",
			ruleIDs: []string{"missing.rule"},
			rules:   nil,
		},
		{
			name:    "registry returns no compiled rules",
			text:    "token=secret",
			ruleIDs: []string{"test.rule"},
			rules:   []registry.CompiledRule{},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			scanner := scannerWithMockRegistry(t, tt.ruleIDs, tt.rules)

			matches, err := scanner.Scan(tt.text, tt.ruleIDs)

			require.NoError(t, err)
			assert.Nil(t, matches)
		})
	}
}

func TestScannerScan_MatchFields(t *testing.T) {
	t.Parallel()

	ruleIDs := []string{"secrets.token"}
	scanner := scannerForRules(t, ruleIDs, rule.Rule{
		ID:       "secrets.token",
		Name:     "token",
		DataType: 42,
		Regex:    `token=([A-Z0-9]+)`,
		Masking: rule.MaskingConfig{
			CaptureGroups: []int{1},
			Placeholder:   "TOKEN",
		},
	})

	matches, err := scanner.Scan("prefix token=ABC123 suffix", ruleIDs)

	require.NoError(t, err)
	require.Equal(t, []sensitive.Match{
		{
			RuleID:      "secrets.token",
			DataType:    42,
			Start:       13,
			End:         19,
			FullText:    "ABC123",
			Placeholder: "TOKEN",
		},
	}, matches)
}

func TestScannerScan_UsesByteOffsets(t *testing.T) {
	t.Parallel()

	ruleIDs := []string{"unicode.token"}
	scanner := scannerForRules(t, ruleIDs, rule.Rule{
		ID:       "unicode.token",
		Name:     "unicode token",
		DataType: 7,
		Regex:    `секрет=([a-z]+)`,
		Masking: rule.MaskingConfig{
			CaptureGroups: []int{1},
			Placeholder:   "SECRET",
		},
	})
	text := "до секрет=value после"

	matches, err := scanner.Scan(text, ruleIDs)

	require.NoError(t, err)
	require.Len(t, matches, 1)
	assert.Equal(t, "value", matches[0].FullText)
	assert.Equal(t, len("до секрет="), matches[0].Start)
	assert.Equal(t, len("до секрет=value"), matches[0].End)
	assert.Equal(t, text[matches[0].Start:matches[0].End], matches[0].FullText)
}

func TestScannerScan_CaptureGroups(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		regex         string
		captureGroups []int
		text          string
		want          []sensitive.Match
	}{
		{
			name:  "full match keeps delimiter",
			regex: `secret;`,
			text:  "x secret; y",
			want: []sensitive.Match{
				{
					RuleID:      "test.rule",
					DataType:    99,
					Start:       2,
					End:         9,
					FullText:    "secret;",
					Placeholder: "TEST",
				},
			},
		},
		{
			name:          "single capture group selects semantic value",
			regex:         `token=([a-z]+);`,
			captureGroups: []int{1},
			text:          "token=abc;",
			want: []sensitive.Match{
				{
					RuleID:      "test.rule",
					DataType:    99,
					Start:       6,
					End:         9,
					FullText:    "abc",
					Placeholder: "TEST",
				},
			},
		},
		{
			name:          "multiple capture groups select first matched configured group",
			regex:         `key=([a-z]+)|standalone-([0-9]+)`,
			captureGroups: []int{1, 2},
			text:          "standalone-42",
			want: []sensitive.Match{
				{
					RuleID:      "test.rule",
					DataType:    99,
					Start:       11,
					End:         13,
					FullText:    "42",
					Placeholder: "TEST",
				},
			},
		},
		{
			name:          "unmatched configured group skips match",
			regex:         `a([a])|(b)`,
			captureGroups: []int{2},
			text:          "aa",
			want:          nil,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ruleIDs := []string{"test.rule"}
			scanner := scannerForRules(t, ruleIDs, rule.Rule{
				ID:       "test.rule",
				Name:     "test rule",
				DataType: 99,
				Regex:    tt.regex,
				Masking: rule.MaskingConfig{
					CaptureGroups: tt.captureGroups,
					Placeholder:   "TEST",
				},
			})

			matches, err := scanner.Scan(tt.text, ruleIDs)

			require.NoError(t, err)
			assert.Equal(t, tt.want, matches)
		})
	}
}

func TestScannerScan_Filtering(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rl   rule.Rule
		text string
		want []sensitive.Match
	}{
		{
			name: "no regex hit returns nil",
			rl: rule.Rule{
				ID:    "filter.none",
				Name:  "none",
				Regex: `secret`,
				Masking: rule.MaskingConfig{
					Placeholder: "SECRET",
				},
			},
			text: "public",
			want: nil,
		},
		{
			name: "empty full match is skipped",
			rl: rule.Rule{
				ID:    "filter.empty",
				Name:  "empty",
				Regex: `a*`,
				Masking: rule.MaskingConfig{
					Placeholder: "EMPTY",
				},
			},
			text: "",
			want: nil,
		},
		{
			name: "capture shorter than min length is skipped",
			rl: rule.Rule{
				ID:        "filter.min_length",
				Name:      "min length",
				Regex:     `token=([A-Z0-9]+)`,
				MinLength: 5,
				Masking: rule.MaskingConfig{
					CaptureGroups: []int{1},
					Placeholder:   "TOKEN",
				},
			},
			text: "token=AB12",
			want: nil,
		},
		{
			name: "validator rejection is skipped",
			rl: rule.Rule{
				ID:         "filter.email.invalid",
				Name:       "invalid email",
				Regex:      `[^\s]+@[^\s]+`,
				Validators: []rule.ValidatorType{rule.ValidatorEmailASCII},
				Masking: rule.MaskingConfig{
					Placeholder: "EMAIL",
				},
			},
			text: "person@example",
			want: nil,
		},
		{
			name: "validator acceptance keeps match",
			rl: rule.Rule{
				ID:         "filter.email.valid",
				Name:       "valid email",
				DataType:   8,
				Regex:      `[^\s]+@[^\s]+`,
				Validators: []rule.ValidatorType{rule.ValidatorEmailASCII},
				Masking: rule.MaskingConfig{
					Placeholder: "EMAIL",
				},
			},
			text: "person.name+test@example.com",
			want: []sensitive.Match{
				{
					RuleID:      "filter.email.valid",
					DataType:    8,
					Start:       0,
					End:         28,
					FullText:    "person.name+test@example.com",
					Placeholder: "EMAIL",
				},
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ruleIDs := []string{tt.rl.ID}
			scanner := scannerForRules(t, ruleIDs, tt.rl)

			matches, err := scanner.Scan(tt.text, ruleIDs)

			require.NoError(t, err)
			assert.Equal(t, tt.want, matches)
		})
	}
}

func TestScannerScan_KeywordPrefilterDisabled(t *testing.T) {
	t.Parallel()

	// With the pre-filter off (the default), a rule fires on a regex hit even
	// when none of its keywords appear in the text — recall is unchanged.
	ruleIDs := []string{"prefilter.off"}
	scanner := scannerForRules(t, ruleIDs, rule.Rule{
		ID:       "prefilter.off",
		Name:     "prefilter off",
		DataType: 3,
		Regex:    `secret=([a-z]+)`,
		Keywords: []string{"token"}, // absent from the text below
		Masking: rule.MaskingConfig{
			CaptureGroups: []int{1},
			Placeholder:   "SECRET",
		},
	})

	matches, err := scanner.Scan("secret=abc", ruleIDs)

	require.NoError(t, err)
	require.Len(t, matches, 1)
	assert.Equal(t, "abc", matches[0].FullText)
}

func TestScannerScan_KeywordPrefilterEligible(t *testing.T) {
	t.Parallel()

	// For a rule whose regex guarantees the keyword (here "secret" is a literal
	// in the regex), the rule is pre-filter-eligible and matching is unaffected.
	tests := []struct {
		name     string
		keywords []string
	}{
		{name: "keyword literal in regex", keywords: []string{"secret"}},
		{name: "no keywords always scans", keywords: nil},
		{name: "keyword case-insensitive vs regex", keywords: []string{"SECRET"}},
	}

	want := []sensitive.Match{{
		RuleID:      "prefilter.rule",
		DataType:    3,
		Start:       7,
		End:         10,
		FullText:    "abc",
		Placeholder: "SECRET",
	}}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ruleIDs := []string{"prefilter.rule"}
			scanner := scannerForRulesWithPrefilter(t, ruleIDs, rule.Rule{
				ID:       "prefilter.rule",
				Name:     "prefilter rule",
				DataType: 3,
				Regex:    `secret=([a-z]+)`,
				Keywords: tt.keywords,
				Masking: rule.MaskingConfig{
					CaptureGroups: []int{1},
					Placeholder:   "SECRET",
				},
			})

			matches, err := scanner.Scan("secret=abc", ruleIDs)

			require.NoError(t, err)
			assert.Equal(t, want, matches)
		})
	}
}

// TestScannerScan_KeywordPrefilterRecallPreserving is the core safety property:
// the pre-filter must never change what Scan finds. For a rule whose keyword the
// regex does NOT guarantee (an external context word), the rule is ineligible
// and always scanned, so a value that matches the regex but lacks the keyword is
// still found — no fail-open. For an eligible rule the outcome is identical by
// construction. Every case here matches the regex but the keyword is absent from
// the text.
func TestScannerScan_KeywordPrefilterRecallPreserving(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rl   rule.Rule
		text string
	}{
		{
			name: "ineligible: external keyword (vault-like)",
			rl: rule.Rule{
				ID:       "recall.vault",
				Regex:    `(hv[sbr]\.[a-z0-9]{5})`,
				Keywords: []string{"vault"}, // never appears in the token itself
				Masking:  rule.MaskingConfig{Placeholder: "VAULT"},
			},
			text: "here is a token=hvs.ab3cd in my config",
		},
		{
			name: "ineligible: bare number, keyword is a label (inn-like)",
			rl: rule.Rule{
				ID:       "recall.inn",
				Regex:    `\b(\d{4})\b`,
				Keywords: []string{"инн"},
				Masking:  rule.MaskingConfig{Placeholder: "NUM"},
			},
			text: "1234",
		},
		{
			name: "ineligible: alternation branch without keyword (passport-like)",
			rl: rule.Rule{
				ID:       "recall.passport",
				Regex:    `(?:passport|pass\.?)\s*(\d{4})`,
				Keywords: []string{"passport"}, // "pass." branch lacks it
				Masking: rule.MaskingConfig{
					CaptureGroups: []int{1},
					Placeholder:   "PASSPORT",
				},
			},
			text: "pass. 4509",
		},
		{
			name: "eligible: keyword guaranteed by regex, present",
			rl: rule.Rule{
				ID:       "recall.eligible",
				Regex:    `secret=([a-z]+)`,
				Keywords: []string{"secret"},
				Masking: rule.MaskingConfig{
					CaptureGroups: []int{1},
					Placeholder:   "SECRET",
				},
			},
			text: "secret=abc",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ruleIDs := []string{tt.rl.ID}
			off, err := scannerForRules(t, ruleIDs, tt.rl).Scan(tt.text, ruleIDs)
			require.NoError(t, err)
			on, err := scannerForRulesWithPrefilter(t, ruleIDs, tt.rl).Scan(tt.text, ruleIDs)
			require.NoError(t, err)

			require.NotEmpty(t, off, "regex should have matched — otherwise the test is vacuous")
			assert.Equal(t, off, on, "keyword pre-filter must be recall-preserving (on == off)")
		})
	}
}

// TestScannerScan_KeywordPrefilterMixedRules exercises the in-place compaction
// drop path with a mix of rules in one Scan call: an eligible rule whose
// keyword is present (kept, fires), an eligible rule whose keyword is absent
// (pre-filtered out — its regex could not have matched anyway, so recall is
// preserved), and a keyword-less rule that is always scanned (kept, fires).
func TestScannerScan_KeywordPrefilterMixedRules(t *testing.T) {
	t.Parallel()

	ruleIDs := []string{"kw.present", "kw.absent", "kw.none"}
	scanner := scannerForRulesWithPrefilter(t, ruleIDs,
		rule.Rule{
			ID: "kw.present", Name: "present", DataType: 1,
			Regex: `secret=([a-z]+)`, Keywords: []string{"secret"},
			Masking: rule.MaskingConfig{CaptureGroups: []int{1}, Placeholder: "A"},
		},
		rule.Rule{
			ID: "kw.absent", Name: "absent", DataType: 2,
			Regex: `apikey=([a-z]+)`, Keywords: []string{"apikey"},
			Masking: rule.MaskingConfig{CaptureGroups: []int{1}, Placeholder: "B"},
		},
		rule.Rule{
			ID: "kw.none", Name: "none", DataType: 3,
			Regex:   `token=([a-z]+)`, // no keywords → always scanned
			Masking: rule.MaskingConfig{CaptureGroups: []int{1}, Placeholder: "C"},
		},
	)

	// "apikey" is absent, so kw.absent is dropped; kw.present and kw.none fire.
	matches, err := scanner.Scan("secret=abc token=xyz", ruleIDs)
	require.NoError(t, err)

	fired := map[string]string{}
	for _, m := range matches {
		fired[m.RuleID] = m.FullText
	}
	assert.Equal(t, map[string]string{"kw.present": "abc", "kw.none": "xyz"}, fired)
}

// TestScannerScan_KeywordPrefilterUnicodeRecall guards the fold fix end-to-end:
// a case-insensitive rule keyed on a non-ASCII multi-fold keyword is ineligible
// for pre-filtering, so it is always scanned and still masks a folded variant
// (final sigma ς) in the body that strings.ToLower would not have matched.
func TestScannerScan_KeywordPrefilterUnicodeRecall(t *testing.T) {
	t.Parallel()

	rl := rule.Rule{
		ID: "unicode.sigma", Name: "sigma", DataType: 1,
		Regex: `(?i)ΣΤΑ`, Keywords: []string{"ΣΤΑ"},
		Masking: rule.MaskingConfig{Placeholder: "SIGMA"},
	}
	ruleIDs := []string{rl.ID}

	// Body contains the final-sigma fold "ςτα" that RE2 matches but
	// strings.ToLower("ΣΤΑ")="στα" would not find.
	const body = "value ςτα here"
	off, err := scannerForRules(t, ruleIDs, rl).Scan(body, ruleIDs)
	require.NoError(t, err)
	on, err := scannerForRulesWithPrefilter(t, ruleIDs, rl).Scan(body, ruleIDs)
	require.NoError(t, err)

	require.NotEmpty(t, off, "regex should match the folded variant")
	assert.Equal(t, off, on, "pre-filter must not drop the Unicode-folded match")
}

func TestScannerScan_InvalidCaptureGroupIndexesAreSkipped(t *testing.T) {
	t.Parallel()

	ruleIDs := []string{"invalid.capture"}
	scanner := scannerWithMockRegistry(t, ruleIDs, []registry.CompiledRule{
		compiledRule(t, rule.Rule{
			ID:    "invalid.capture",
			Name:  "invalid capture",
			Regex: `token=([a-z]+)`,
			Masking: rule.MaskingConfig{
				CaptureGroups: []int{2},
				Placeholder:   "TOKEN",
			},
		}),
	})

	matches, err := scanner.Scan("token=abc", ruleIDs)

	require.NoError(t, err)
	assert.Nil(t, matches)
}

func TestScannerScan_ResolveConflicts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		rules []rule.Rule
		text  string
		want  []sensitive.Match
	}{
		{
			name: "same start keeps longest match",
			rules: []rule.Rule{
				testRule("conflict.short", `abc`, 1, "SHORT"),
				testRule("conflict.long", `abcdef`, 2, "LONG"),
			},
			text: "abcdef",
			want: []sensitive.Match{
				{
					RuleID:      "conflict.long",
					DataType:    2,
					Start:       0,
					End:         6,
					FullText:    "abcdef",
					Placeholder: "LONG",
				},
			},
		},
		{
			name: "same span keeps lexicographically first rule id",
			rules: []rule.Rule{
				testRule("z.rule", `secret`, 1, "Z"),
				testRule("a.rule", `secret`, 2, "A"),
			},
			text: "secret",
			want: []sensitive.Match{
				{
					RuleID:      "a.rule",
					DataType:    2,
					Start:       0,
					End:         6,
					FullText:    "secret",
					Placeholder: "A",
				},
			},
		},
		{
			name: "partially overlapping matches coalesce into a union span",
			rules: []rule.Rule{
				testRule("conflict.left", `abc`, 1, "LEFT"),
				testRule("conflict.right", `bcd`, 2, "RIGHT"),
			},
			text: "abcd",
			// abc[0,3) and bcd[1,4) overlap. Dropping either would emit its
			// exclusive byte ("a" or "d") verbatim, leaking part of a detected
			// value, so they merge into [0,4). Equal length → lowest start wins
			// as the representative.
			want: []sensitive.Match{
				{
					RuleID:      "conflict.left",
					DataType:    1,
					Start:       0,
					End:         4,
					FullText:    "abcd",
					Placeholder: "LEFT",
				},
			},
		},
		{
			name: "different-length partial overlap masks the union, not just the longer match",
			rules: []rule.Rule{
				testRule("overlap.short", `abcd`, 1, "SHORT"),
				testRule("overlap.long", `cdefgh`, 2, "LONG"),
			},
			text: "abcdefgh",
			// short[0,4) and long[2,8) overlap; the union [0,8) covers the short
			// match's exclusive prefix "ab" that a plain longest-first drop would
			// leak. The longer constituent is the representative.
			want: []sensitive.Match{
				{
					RuleID:      "overlap.long",
					DataType:    2,
					Start:       0,
					End:         8,
					FullText:    "abcdefgh",
					Placeholder: "LONG",
				},
			},
		},
		{
			name: "chain of overlaps coalesces transitively",
			rules: []rule.Rule{
				testRule("chain.a", `abcd`, 1, "A"),
				testRule("chain.b", `cdef`, 2, "B"),
				testRule("chain.c", `efgh`, 3, "C"),
			},
			text: "abcdefgh",
			// a[0,4) overlaps b[2,6) overlaps c[4,8); a does not reach c, but the
			// run extends transitively via the growing end, so the whole [0,8)
			// is one union. All three are equal length → lowest start wins.
			want: []sensitive.Match{
				{
					RuleID:      "chain.a",
					DataType:    1,
					Start:       0,
					End:         8,
					FullText:    "abcdefgh",
					Placeholder: "A",
				},
			},
		},
		{
			name: "adjacent matches are kept",
			rules: []rule.Rule{
				testRule("adjacent.left", `abc`, 1, "LEFT"),
				testRule("adjacent.right", `def`, 2, "RIGHT"),
			},
			text: "abcdef",
			want: []sensitive.Match{
				{
					RuleID:      "adjacent.left",
					DataType:    1,
					Start:       0,
					End:         3,
					FullText:    "abc",
					Placeholder: "LEFT",
				},
				{
					RuleID:      "adjacent.right",
					DataType:    2,
					Start:       3,
					End:         6,
					FullText:    "def",
					Placeholder: "RIGHT",
				},
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ruleIDs := make([]string, 0, len(tt.rules))
			for _, rl := range tt.rules {
				ruleIDs = append(ruleIDs, rl.ID)
			}
			scanner := scannerForRules(t, ruleIDs, tt.rules...)

			matches, err := scanner.Scan(tt.text, ruleIDs)

			require.NoError(t, err)
			assert.Equal(t, tt.want, matches)
		})
	}
}

func scannerWithMockRegistry(
	t *testing.T,
	expectedRuleIDs []string,
	rules []registry.CompiledRule,
	opts ...sensitive.Option,
) *sensitive.Scanner {
	t.Helper()

	ctrl := gomock.NewController(t)
	reg := NewMockRegistry(ctrl)
	reg.EXPECT().GetCompiledRulesByRuleIDs(expectedRuleIDs).Return(rules)
	return sensitive.New(reg, opts...)
}

func scannerForRules(t *testing.T, expectedRuleIDs []string, rules ...rule.Rule) *sensitive.Scanner {
	t.Helper()

	return scannerWithMockRegistry(t, expectedRuleIDs, compiledRulesFromRegistry(t, rules...))
}

func scannerForRulesWithPrefilter(t *testing.T, expectedRuleIDs []string, rules ...rule.Rule) *sensitive.Scanner {
	t.Helper()

	return scannerWithMockRegistry(
		t,
		expectedRuleIDs,
		compiledRulesFromRegistry(t, rules...),
		sensitive.WithKeywordPrefilter(true),
	)
}

func compiledRulesFromRegistry(t *testing.T, rules ...rule.Rule) []registry.CompiledRule {
	t.Helper()

	reg := registry.NewRegistry()
	reg.Register(rules...)

	ruleIDs := make([]string, 0, len(rules))
	for _, rl := range rules {
		ruleIDs = append(ruleIDs, rl.ID)
	}

	return reg.GetCompiledRulesByRuleIDs(ruleIDs)
}

func compiledRule(t *testing.T, rl rule.Rule) registry.CompiledRule {
	t.Helper()

	re, err := regexp.Compile("(?m)" + rl.Regex)
	require.NoError(t, err)

	return registry.CompiledRule{
		Rule: rl,
		Re:   re,
	}
}

func testRule(id, pattern string, dataType int, placeholder string) rule.Rule {
	return rule.Rule{
		ID:       id,
		Name:     id,
		DataType: dataType,
		Regex:    pattern,
		Masking: rule.MaskingConfig{
			Placeholder: placeholder,
		},
	}
}

// TestScanRules_WorkerPanicRecoveredAsError verifies the fail-open invariant:
// a panic inside a parallel scan worker (here, a compiled rule with a nil Re)
// is recovered and surfaced as an error instead of crashing the whole process.
// The gRPC recovery interceptor does not cover child goroutines, so without the
// in-worker recover this would take down every in-flight stream on the replica.
func TestScanRules_WorkerPanicRecoveredAsError(t *testing.T) {
	t.Parallel()

	// > parallelRuleThreshold rules and a > parallelTextThreshold text force the
	// fan-out path where the panic happens in a child goroutine.
	rules := []registry.CompiledRule{
		compiledRule(t, testRule("r1", "aaa", 1, "<A_>")),
		compiledRule(t, testRule("r2", "bbb", 1, "<B_>")),
		compiledRule(t, testRule("r3", "ccc", 1, "<C_>")),
		compiledRule(t, testRule("r4", "ddd", 1, "<D_>")),
		compiledRule(t, testRule("r5", "eee", 1, "<E_>")),
		{Rule: testRule("boom", "unused", 1, "<X_>")}, // Re is nil -> panics in scanRule
	}
	text := strings.Repeat("x", 5*1024)

	s := sensitive.New(nil) // registry unused by ScanRules

	require.NotPanics(t, func() {
		_, err := s.ScanRules(text, rules)
		assert.Error(t, err, "worker panic must surface as an error, not a crash")
	})
}

// TestScanRules_SequentialAndParallelPathsAgree exercises both sides of the
// size gate (#5): the same rule set over a small text (sequential) and a large
// text (parallel) must find the same value.
func TestScanRules_SequentialAndParallelPathsAgree(t *testing.T) {
	t.Parallel()

	// Six rules so the large-text case clears parallelRuleThreshold and fans out.
	rules := []registry.CompiledRule{
		compiledRule(t, testRule("r1", "SECRET", 1, "<S>")),
		compiledRule(t, testRule("r2", "zzz1", 1, "<Z1>")),
		compiledRule(t, testRule("r3", "zzz2", 1, "<Z2>")),
		compiledRule(t, testRule("r4", "zzz3", 1, "<Z3>")),
		compiledRule(t, testRule("r5", "zzz4", 1, "<Z4>")),
		compiledRule(t, testRule("r6", "zzz5", 1, "<Z5>")),
	}
	s := sensitive.New(nil)

	small := "prefix SECRET suffix" // < parallelTextThreshold -> sequential
	large := strings.Repeat("p", 5*1024) + " SECRET"

	smallMatches, err := s.ScanRules(small, rules)
	require.NoError(t, err)
	require.Len(t, smallMatches, 1)
	assert.Equal(t, "SECRET", smallMatches[0].FullText)

	largeMatches, err := s.ScanRules(large, rules)
	require.NoError(t, err)
	require.Len(t, largeMatches, 1)
	assert.Equal(t, "SECRET", largeMatches[0].FullText)
}
