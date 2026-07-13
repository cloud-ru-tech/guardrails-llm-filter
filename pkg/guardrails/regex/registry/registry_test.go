package registry

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
)

func TestRegistry_Lookups(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	reg.Register(
		testRule("pii.email", 20, "EMAIL"),
		testRule("tokens.api", 10, "API_TOKEN", "secret", "token"),
		testRule("pii.phone", 20, "PHONE"),
		testRule("empty.placeholder", 30, ""),
	)

	t.Run("get rules by ids preserves requested order and skips unknown ids", func(t *testing.T) {
		t.Parallel()

		got := reg.GetRulesByIDs("tokens.api", "missing.rule", "pii.email")

		require.Len(t, got, 2)
		assert.Equal(t, "tokens.api", got[0].ID)
		assert.Equal(t, "pii.email", got[1].ID)
		assert.Nil(t, reg.GetRulesByIDs())
	})

	t.Run("get rule ids by data types returns sorted ids", func(t *testing.T) {
		t.Parallel()

		got := reg.GetRuleIDsByDataTypes(20, 10, 404)

		assert.Equal(t, []string{"pii.email", "pii.phone", "tokens.api"}, got)
		assert.Nil(t, reg.GetRuleIDsByDataTypes())
	})

	t.Run("get rules by data types preserves data type and registration order", func(t *testing.T) {
		t.Parallel()

		got := reg.GetRulesByDataTypes(20, 10, 404)

		require.Len(t, got, 3)
		assert.Equal(t, []string{"pii.email", "pii.phone", "tokens.api"}, ruleIDs(got))
		assert.Nil(t, reg.GetRulesByDataTypes())
	})

	t.Run("get masking placeholder by rule id", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, "EMAIL", reg.GetMaskingPlaceholderByRuleID("pii.email"))
		assert.Empty(t, reg.GetMaskingPlaceholderByRuleID("missing.rule"))
	})

	t.Run("has rules for data types", func(t *testing.T) {
		t.Parallel()

		assert.True(t, reg.HasRulesForDataTypes([]uint32{404, 10}))
		assert.False(t, reg.HasRulesForDataTypes([]uint32{404, 405}))
		assert.False(t, reg.HasRulesForDataTypes(nil))
	})

	t.Run("get compiled rules by data types returns keyword flag", func(t *testing.T) {
		t.Parallel()

		got, hasKeywords := reg.GetCompiledRulesByDataTypes([]uint32{20})
		require.Len(t, got, 2)
		assert.False(t, hasKeywords)
		assert.Equal(t, []string{"pii.email", "pii.phone"}, compiledRuleIDs(got))

		got, hasKeywords = reg.GetCompiledRulesByDataTypes([]uint32{10, 404})
		require.Len(t, got, 1)
		assert.True(t, hasKeywords)
		assert.Equal(t, "tokens.api", got[0].ID)

		got, hasKeywords = reg.GetCompiledRulesByDataTypes(nil)
		assert.Nil(t, got)
		assert.False(t, hasKeywords)
	})

	t.Run("get compiled rules by rule ids preserves requested order and compiled fields", func(t *testing.T) {
		t.Parallel()

		got := reg.GetCompiledRulesByRuleIDs([]string{"pii.phone", "missing.rule", "empty.placeholder"})

		require.Len(t, got, 2)
		assert.Equal(t, []string{"pii.phone", "empty.placeholder"}, compiledRuleIDs(got))
		require.NotNil(t, got[0].Re)
		require.NotNil(t, got[0].PlaceholderRe)
		assert.Positive(t, got[0].PlaceholderLen)
		require.NotNil(t, got[1].Re)
		assert.Nil(t, got[1].PlaceholderRe)
		assert.Zero(t, got[1].PlaceholderLen)
		assert.Nil(t, reg.GetCompiledRulesByRuleIDs(nil))
	})
}

func TestRegistry_PlaceholderRegex(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	reg.Register(
		testRule("pii.email", 1, "EMAIL"),
		testRule("access.token", 1, "ACCESS_TOKEN"),
		testRule("blank.placeholder", 1, "   "),
	)

	emailRule := reg.GetCompiledRulesByRuleIDs([]string{"pii.email"})[0]
	matches := emailRule.PlaceholderRe.FindStringSubmatchIndex("before < eMaIl-002 > after")
	require.Len(t, matches, 4)
	assert.Equal(t, "002", "before < eMaIl-002 > after"[matches[2]:matches[3]])

	tokenRule := reg.GetCompiledRulesByRuleIDs([]string{"access.token"})[0]
	matches = tokenRule.PlaceholderRe.FindStringSubmatchIndex("before <ACCESS-  token__123> after")
	require.Len(t, matches, 4)
	assert.Equal(t, "123", "before <ACCESS-  token__123> after"[matches[2]:matches[3]])

	blankRule := reg.GetCompiledRulesByRuleIDs([]string{"blank.placeholder"})[0]
	assert.Nil(t, blankRule.PlaceholderRe)
	assert.Zero(t, blankRule.PlaceholderLen)

	wantMaxLen := max(emailRule.PlaceholderLen, tokenRule.PlaceholderLen)
	assert.Equal(t, wantMaxLen, reg.GetMaxPlaceholderLenByRuleIDs("missing.rule", "pii.email", "access.token"))
	assert.Zero(t, reg.GetMaxPlaceholderLenByRuleIDs("missing.rule"))
}

func TestRegexpMaxLen(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		pattern string
		want    int
	}{
		{
			name:    "literal",
			pattern: `abc`,
			want:    3,
		},
		{
			name:    "unicode literal uses byte length",
			pattern: `ёж`,
			want:    len("ёж"),
		},
		{
			name:    "bounded repeat",
			pattern: `a{2,4}`,
			want:    4,
		},
		{
			name:    "optional bounded subexpression",
			pattern: `(ab)?`,
			want:    2,
		},
		{
			name:    "alternation uses longest branch",
			pattern: `ab|cde`,
			want:    3,
		},
		{
			name:    "char class uses maximum rune byte length",
			pattern: `[AЯ]`,
			want:    len("Я"),
		},
		{
			name:    "any char uses utf8 max",
			pattern: `.`,
			want:    utf8.UTFMax,
		},
		{
			name:    "star is unbounded",
			pattern: `a*`,
			want:    0,
		},
		{
			name:    "plus is unbounded",
			pattern: `a+`,
			want:    0,
		},
		{
			name:    "open repeat is unbounded",
			pattern: `a{2,}`,
			want:    0,
		},
		{
			name:    "invalid regexp returns zero",
			pattern: `[`,
			want:    0,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, regexpMaxLen(tt.pattern))
		})
	}
}

func TestMaxRuneLenInClass_InvalidRuneFallsBackToUTFMax(t *testing.T) {
	t.Parallel()

	assert.Equal(t, utf8.UTFMax, maxRuneLenInClass([]rune{0, utf8.MaxRune + 1}))
}

func TestRegister_ValidatesRuleConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		register    func(reg *Registry)
		panicSubstr string
	}{
		{
			name: "empty rule id",
			register: func(reg *Registry) {
				reg.Register(rule.Rule{Name: "empty id", Regex: `secret`})
			},
			panicSubstr: "empty rule_id",
		},
		{
			name: "duplicate rule id",
			register: func(reg *Registry) {
				reg.Register(testRule("duplicate.rule", 1, "DUP"))
				reg.Register(testRule("duplicate.rule", 1, "DUP"))
			},
			panicSubstr: "duplicate rule_id",
		},
		{
			name: "unsupported validator",
			register: func(reg *Registry) {
				rl := testRule("bad.validator", 1, "BAD")
				rl.Validators = []rule.ValidatorType{"unknown_validator"}
				reg.Register(rl)
			},
			panicSubstr: "unsupported validator",
		},
		{
			name: "invalid regexp",
			register: func(reg *Registry) {
				rl := testRule("bad.regex", 1, "BAD")
				rl.Regex = `[`
				reg.Register(rl)
			},
			panicSubstr: "regex",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reg := NewRegistry()
			assertPanicsWithErrorContaining(t, tt.panicSubstr, func() {
				tt.register(reg)
			})
		})
	}
}

func TestRegister_ValidatesCaptureGroups(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		regex       string
		groups      []int
		wantPanic   bool
		panicSubstr string
	}{
		{
			name:   "empty capture groups means full match",
			regex:  `secret`,
			groups: nil,
		},
		{
			name:   "valid capture groups",
			regex:  `(a)|(b)`,
			groups: []int{1, 2},
		},
		{
			name:        "zero capture group",
			regex:       `(a)`,
			groups:      []int{0},
			wantPanic:   true,
			panicSubstr: "positive indexes",
		},
		{
			name:        "capture group exceeds regexp groups",
			regex:       `(a)`,
			groups:      []int{2},
			wantPanic:   true,
			panicSubstr: "exceeds regex capture groups",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			register := func() {
				reg := NewRegistry()
				reg.Register(rule.Rule{
					ID:    "test.rule",
					Name:  "test.rule",
					Regex: tt.regex,
					Masking: rule.MaskingConfig{
						CaptureGroups: tt.groups,
						Placeholder:   "TEST",
					},
				})
			}

			if !tt.wantPanic {
				register()
				return
			}

			assertPanicsWithErrorContaining(t, tt.panicSubstr, register)
		})
	}
}

func assertPanicsWithErrorContaining(t *testing.T, wantSubstr string, fn func()) {
	t.Helper()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic, got nil")
		}
		err, ok := r.(error)
		if !ok {
			t.Fatalf("panic type=%T, want error", r)
		}
		if !strings.Contains(err.Error(), wantSubstr) {
			t.Fatalf("panic=%v, want substring %q", err, wantSubstr)
		}
	}()
	fn()
}

func TestCompileRule_PrefilterKeywords(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		regex    string
		keywords []string
		want     []string // nil = ineligible / always scanned
	}{
		{
			name:     "keyword is a literal in the regex",
			regex:    `secret=([a-z]+)`,
			keywords: []string{"secret"},
			want:     []string{"secret"},
		},
		{
			name:     "vendor context literal (gitleaks style)",
			regex:    `(?i)[\w.-]{0,50}?(?:adobe)[\s'"]{0,3}([a-f0-9]{32})`,
			keywords: []string{"adobe"},
			want:     []string{"adobe"},
		},
		{
			name:     "every alternation branch contains the keyword",
			regex:    `(?:tokenA|tokenB)-[0-9]+`,
			keywords: []string{"token"},
			want:     []string{"token"},
		},
		{
			name:     "stored keyword is lowercased",
			regex:    `secret=([a-z]+)`,
			keywords: []string{"SECRET"},
			want:     []string{"secret"},
		},
		{
			name:     "no keywords is never prefilterable",
			regex:    `secret=([a-z]+)`,
			keywords: nil,
			want:     nil,
		},
		// The fail-open cases the code review flagged: the keyword is an external
		// label the regex does not require, so the rule must NOT be prefiltered.
		{
			name:     "external keyword absent from regex (vault-like)",
			regex:    `(hv[sbr]\.[A-Za-z0-9_\-]{90,120})`,
			keywords: []string{"vault", "VAULT_TOKEN"},
			want:     nil,
		},
		{
			name:     "self-contained hex token (twilio-like)",
			regex:    `\b([a-f0-9]{32})\b`,
			keywords: []string{"twilio", "auth_token"},
			want:     nil,
		},
		{
			name:     "bare checksum number (inn-like)",
			regex:    `\b\d{12}\b`,
			keywords: []string{"инн", "inn"},
			want:     nil,
		},
		{
			name:     "alternation branch without the keyword (passport-like)",
			regex:    `(?:паспорт|пасп\.?|серия)\s*(\d{6})`,
			keywords: []string{"паспорт", "серия", "номер"},
			want:     nil,
		},
		// Unicode case-fold guard: strings.ToLower (runtime pre-filter) must not
		// diverge from RE2 folding, or a real match could be dropped.
		{
			// ASCII case-insensitive literal is safe: RE2 folding and
			// strings.ToLower agree for a..z, so it stays eligible.
			name:     "case-insensitive ASCII literal stays eligible",
			regex:    `(?i)bearer\s+([a-z0-9]+)`,
			keywords: []string{"bearer"},
			want:     []string{"bearer"},
		},
		{
			// Greek sigma folds Σ/σ/ς; strings.ToLower keeps final-sigma ς
			// distinct from σ, so RE2 can match text the pre-filter would drop.
			name:     "case-insensitive non-ASCII multi-fold literal is ineligible",
			regex:    `(?i)ΣΤΑ([0-9]+)`,
			keywords: []string{"ΣΤΑ"},
			want:     nil,
		},
		{
			// Long-s ſ folds with ASCII 's' under RE2 but strings.ToLower does
			// not normalise it back, so a case-insensitive 's' keyword diverges.
			name:     "case-insensitive keyword with long-s cross-fold is ineligible",
			regex:    `(?i)secret=([a-z]+)`,
			keywords: []string{"secret"},
			want:     nil,
		},
		{
			// Non-ASCII literal WITHOUT case-insensitivity is safe: the literal
			// matches verbatim and both sides go through strings.ToLower.
			name:     "case-sensitive non-ASCII literal stays eligible",
			regex:    `паспорт\s*(\d{6})`,
			keywords: []string{"паспорт"},
			want:     []string{"паспорт"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cr, err := CompileRule(NewRegistry(), rule.Rule{
				ID:       "test.rule",
				Name:     "test rule",
				Regex:    tt.regex,
				Keywords: tt.keywords,
				Masking:  rule.MaskingConfig{Placeholder: "T"},
			})
			require.NoError(t, err)
			assert.Equal(t, tt.want, cr.PrefilterKeywords)
		})
	}
}

func TestRegistry_PrefilterIneligibleRuleIDs(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	reg.Register(
		rule.Rule{ID: "elig", Regex: `secret=([a-z]+)`, Keywords: []string{"secret"}, Masking: rule.MaskingConfig{Placeholder: "S"}},
		rule.Rule{ID: "inelig", Regex: `\b([a-f0-9]{32})\b`, Keywords: []string{"twilio"}, Masking: rule.MaskingConfig{Placeholder: "T"}},
		rule.Rule{ID: "nokw", Regex: `[A-Z]+`, Masking: rule.MaskingConfig{Placeholder: "N"}},
	)

	// Only rules that declare keywords but are not eligible are reported.
	assert.Equal(t, []string{"inelig"}, reg.PrefilterIneligibleRuleIDs())
}

// ResolveForDataTypes must gate a CUSTOM (data_type 6) rule strictly on the
// enabled set: excluded when 6 is absent, resolved when present. A custom rule
// silently never scans unless CUSTOM is in the enabled data types (now the
// shipped default).
func TestResolveForDataTypes_CustomGatedByEnabledSet(t *testing.T) {
	const customDataType = 6
	reg := NewRegistry()
	reg.Register(testRule("custom.rule", customDataType, "CUSTOM_1"))

	t.Run("excluded when CUSTOM not enabled", func(t *testing.T) {
		ids, rules := reg.ResolveForDataTypes([]uint32{1, 2, 3, 4, 5})
		assert.Empty(t, ids)
		assert.Empty(t, rules)
	})

	t.Run("resolved when CUSTOM enabled", func(t *testing.T) {
		ids, rules := reg.ResolveForDataTypes([]uint32{1, 2, 3, 4, 5, 6})
		assert.Equal(t, []string{"custom.rule"}, ids)
		assert.Len(t, rules, 1)
	})
}

func testRule(id string, dataType int, placeholder string, keywords ...string) rule.Rule {
	return rule.Rule{
		ID:       id,
		Name:     id,
		DataType: dataType,
		Regex:    `[A-Z]+`,
		Keywords: keywords,
		Masking: rule.MaskingConfig{
			Placeholder: placeholder,
		},
	}
}

func ruleIDs(rules []rule.Rule) []string {
	out := make([]string, 0, len(rules))
	for _, rl := range rules {
		out = append(out, rl.ID)
	}
	return out
}

func compiledRuleIDs(rules []CompiledRule) []string {
	out := make([]string, 0, len(rules))
	for _, rl := range rules {
		out = append(out, rl.ID)
	}
	return out
}
