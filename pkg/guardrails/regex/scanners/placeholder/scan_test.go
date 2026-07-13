package placeholder_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/registry"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/scanners/placeholder"
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
			text:    "<EMAIL_1>",
			ruleIDs: nil,
			rules:   nil,
		},
		{
			name:    "unknown rule id",
			text:    "<EMAIL_1>",
			ruleIDs: []string{"missing.rule"},
			rules:   nil,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			reg := NewMockRegistry(ctrl)
			reg.EXPECT().GetCompiledRulesByRuleIDs(tt.ruleIDs).Return(tt.rules)

			matches, err := placeholder.New(reg).Scan(tt.text, tt.ruleIDs)

			require.NoError(t, err)
			assert.Nil(t, matches)
		})
	}
}

func TestScannerScan_MatchesPlaceholders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		text        string
		placeholder string
		want        []placeholder.Match
	}{
		{
			name:        "canonical placeholder",
			text:        "before <EMAIL_1> after",
			placeholder: "EMAIL",
			want: []placeholder.Match{
				{
					RuleID:      "test.rule",
					Start:       len("before "),
					End:         len("before <EMAIL_1>"),
					Placeholder: "<EMAIL_1>",
				},
			},
		},
		{
			name:        "case and separator variants are canonicalized",
			text:        "before < eMaIl-002 > after",
			placeholder: "EMAIL",
			want: []placeholder.Match{
				{
					RuleID:      "test.rule",
					Start:       len("before "),
					End:         len("before < eMaIl-002 >"),
					Placeholder: "<EMAIL_2>",
				},
			},
		},
		{
			name:        "placeholder type split on underscores allows separator drift",
			text:        "before <ACCESS-  token__12> after",
			placeholder: "ACCESS_TOKEN",
			want: []placeholder.Match{
				{
					RuleID:      "test.rule",
					Start:       len("before "),
					End:         len("before <ACCESS-  token__12>"),
					Placeholder: "<ACCESS_TOKEN_12>",
				},
			},
		},
		{
			name:        "multiple placeholders from one rule",
			text:        "<EMAIL_1> and <email_3>",
			placeholder: "EMAIL",
			want: []placeholder.Match{
				{
					RuleID:      "test.rule",
					Start:       0,
					End:         len("<EMAIL_1>"),
					Placeholder: "<EMAIL_1>",
				},
				{
					RuleID:      "test.rule",
					Start:       len("<EMAIL_1> and "),
					End:         len("<EMAIL_1> and <email_3>"),
					Placeholder: "<EMAIL_3>",
				},
			},
		},
		{
			name:        "byte offsets with multibyte prefix",
			text:        "до <EMAIL_4> после",
			placeholder: "EMAIL",
			want: []placeholder.Match{
				{
					RuleID:      "test.rule",
					Start:       len("до "),
					End:         len("до <EMAIL_4>"),
					Placeholder: "<EMAIL_4>",
				},
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rules := compiledRulesFromRegistry(t, ruleWithPlaceholder("test.rule", tt.placeholder))
			scanner := scannerWithMockRegistry(t, []string{"test.rule"}, rules)

			matches, err := scanner.Scan(tt.text, []string{"test.rule"})

			require.NoError(t, err)
			assert.Equal(t, tt.want, matches)
		})
	}
}

func TestScannerScan_FiltersInvalidCandidates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cr   registry.CompiledRule
		text string
	}{
		{
			name: "nil placeholder regexp",
			cr: registry.CompiledRule{
				Rule: ruleWithPlaceholder("filter.nil_regex", "EMAIL"),
			},
			text: "<EMAIL_1>",
		},
		{
			name: "blank placeholder type",
			cr: registry.CompiledRule{
				Rule: rule.Rule{
					ID:   "filter.blank_placeholder",
					Name: "filter.blank_placeholder",
					Masking: rule.MaskingConfig{
						Placeholder: "   ",
					},
				},
				PlaceholderRe: regexp.MustCompile(`<EMAIL_([0-9]+)>`),
			},
			text: "<EMAIL_1>",
		},
		{
			name: "no placeholder hit",
			cr:   compiledRulesFromRegistry(t, ruleWithPlaceholder("filter.no_hit", "EMAIL"))[0],
			text: "no placeholder here",
		},
		{
			name: "regexp without index capture",
			cr:   customCompiledRule("filter.no_capture", "EMAIL", `<EMAIL>`),
			text: "<EMAIL>",
		},
		{
			name: "unmatched optional index capture",
			cr:   customCompiledRule("filter.unmatched_capture", "EMAIL", `<EMAIL(?:_([0-9]+))?>`),
			text: "<EMAIL>",
		},
		{
			name: "empty index capture",
			cr:   customCompiledRule("filter.empty_capture", "EMAIL", `<EMAIL_([0-9]*)>`),
			text: "<EMAIL_>",
		},
		{
			name: "non numeric index capture",
			cr:   customCompiledRule("filter.non_numeric", "EMAIL", `<EMAIL_([^>]+)>`),
			text: "<EMAIL_abc>",
		},
		{
			name: "zero index capture",
			cr:   compiledRulesFromRegistry(t, ruleWithPlaceholder("filter.zero", "EMAIL"))[0],
			text: "<EMAIL_0>",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			scanner := scannerWithMockRegistry(t, []string{tt.cr.ID}, []registry.CompiledRule{tt.cr})

			matches, err := scanner.Scan(tt.text, []string{tt.cr.ID})

			require.NoError(t, err)
			assert.Nil(t, matches)
		})
	}
}

func TestScannerScan_ResolveConflicts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		rules   []registry.CompiledRule
		ruleIDs []string
		text    string
		want    []placeholder.Match
	}{
		{
			name: "same start keeps longest match",
			rules: []registry.CompiledRule{
				customCompiledRule("conflict.short", "A", `<A_([0-9]+)>`),
				customCompiledRule("conflict.long", "A_LONG", `<A_([0-9]+)>_LONG`),
			},
			ruleIDs: []string{"conflict.short", "conflict.long"},
			text:    "<A_1>_LONG",
			want: []placeholder.Match{
				{
					RuleID:      "conflict.long",
					Start:       0,
					End:         len("<A_1>_LONG"),
					Placeholder: "<A_LONG_1>",
				},
			},
		},
		{
			name: "same span keeps lexicographically first rule id",
			rules: []registry.CompiledRule{
				customCompiledRule("z.rule", "ZED", `<X_([0-9]+)>`),
				customCompiledRule("a.rule", "ALPHA", `<X_([0-9]+)>`),
			},
			ruleIDs: []string{"z.rule", "a.rule"},
			text:    "<X_2>",
			want: []placeholder.Match{
				{
					RuleID:      "a.rule",
					Start:       0,
					End:         len("<X_2>"),
					Placeholder: "<ALPHA_2>",
				},
			},
		},
		{
			name: "later overlapping match is dropped",
			rules: []registry.CompiledRule{
				customCompiledRule("conflict.left", "LEFT", `<A_([0-9]+)>`),
				customCompiledRule("conflict.right", "RIGHT", `1>_([A-Z]+)`),
			},
			ruleIDs: []string{"conflict.left", "conflict.right"},
			text:    "<A_1>_RIGHT",
			want: []placeholder.Match{
				{
					RuleID:      "conflict.left",
					Start:       0,
					End:         len("<A_1>"),
					Placeholder: "<LEFT_1>",
				},
			},
		},
		{
			name: "adjacent matches are kept",
			rules: []registry.CompiledRule{
				customCompiledRule("adjacent.left", "LEFT", `<A_([0-9]+)>`),
				customCompiledRule("adjacent.right", "RIGHT", `<B_([0-9]+)>`),
			},
			ruleIDs: []string{"adjacent.left", "adjacent.right"},
			text:    "<A_1><B_2>",
			want: []placeholder.Match{
				{
					RuleID:      "adjacent.left",
					Start:       0,
					End:         len("<A_1>"),
					Placeholder: "<LEFT_1>",
				},
				{
					RuleID:      "adjacent.right",
					Start:       len("<A_1>"),
					End:         len("<A_1><B_2>"),
					Placeholder: "<RIGHT_2>",
				},
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			scanner := scannerWithMockRegistry(t, tt.ruleIDs, tt.rules)

			matches, err := scanner.Scan(tt.text, tt.ruleIDs)

			require.NoError(t, err)
			assert.Equal(t, tt.want, matches)
		})
	}
}

func scannerWithMockRegistry(
	t *testing.T,
	expectedRuleIDs []string,
	rules []registry.CompiledRule,
) *placeholder.Scanner {
	t.Helper()

	ctrl := gomock.NewController(t)
	reg := NewMockRegistry(ctrl)
	reg.EXPECT().GetCompiledRulesByRuleIDs(expectedRuleIDs).Return(rules)
	return placeholder.New(reg)
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

func ruleWithPlaceholder(id string, placeholderType string) rule.Rule {
	return rule.Rule{
		ID:    id,
		Name:  id,
		Regex: `unused-sensitive-regex`,
		Masking: rule.MaskingConfig{
			Placeholder: placeholderType,
		},
	}
}

func customCompiledRule(id string, placeholderType string, placeholderPattern string) registry.CompiledRule {
	return registry.CompiledRule{
		Rule: rule.Rule{
			ID:   id,
			Name: id,
			Masking: rule.MaskingConfig{
				Placeholder: placeholderType,
			},
		},
		PlaceholderRe: regexp.MustCompile("(?m)" + placeholderPattern),
	}
}

// TestScanRules_ParallelPathFindsMatches exercises the goroutine fan-out
// (> parallelRuleThreshold rules and a > parallelTextThreshold text) after the
// scanParallel signature gained an error return: a placeholder must still be
// found on the parallel path.
func TestScanRules_ParallelPathFindsMatches(t *testing.T) {
	t.Parallel()

	rules := []registry.CompiledRule{
		customCompiledRule("email", "EMAIL", `<EMAIL_(\d+)>`),
		customCompiledRule("r2", "R2", `<R2_(\d+)>`),
		customCompiledRule("r3", "R3", `<R3_(\d+)>`),
		customCompiledRule("r4", "R4", `<R4_(\d+)>`),
		customCompiledRule("r5", "R5", `<R5_(\d+)>`),
		customCompiledRule("r6", "R6", `<R6_(\d+)>`),
	}
	text := strings.Repeat("filler ", 1024) + "<EMAIL_1>" // > 4 KiB -> parallel

	matches, err := placeholder.New(nil).ScanRules(text, rules)
	require.NoError(t, err)
	require.Len(t, matches, 1)
	assert.Equal(t, "email", matches[0].RuleID)
}
