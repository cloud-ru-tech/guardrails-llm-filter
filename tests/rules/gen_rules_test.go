package rules_test

import (
	"fmt"
	"regexp"
	"regexp/syntax"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
)

const (
	expectedGenConfigRules  = 257
	expectedRealConfigCases = 268
)

type genConfigRuleCase struct {
	name         string
	ruleID       string
	input        string
	wantFullText string
}

func TestGenConfigRules_ScanSemanticSpan(t *testing.T) {
	t.Parallel()

	scanner, rulesByID := loadRealConfigScanner(t)
	cases := genConfigRuleCases(t, rulesByID)
	if len(cases) != expectedRealConfigCases {
		t.Fatalf("len(cases)=%d, want %d", len(cases), expectedRealConfigCases)
	}

	for _, tt := range cases {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rl, ok := rulesByID[tt.ruleID]
			if !ok {
				t.Fatalf("rule %q not loaded", tt.ruleID)
			}

			matches, err := scanner.Scan(tt.input, []string{tt.ruleID})
			if err != nil {
				t.Fatalf("Scan error: %v", err)
			}
			if len(matches) != 1 {
				t.Fatalf("len(matches)=%d, want 1; matches=%+v; input=%q", len(matches), matches, tt.input)
			}

			got := matches[0]
			if got.RuleID != tt.ruleID {
				t.Fatalf("RuleID=%q, want %q", got.RuleID, tt.ruleID)
			}
			if got.FullText != tt.wantFullText {
				t.Fatalf("FullText=%q, want %q; input=%q", got.FullText, tt.wantFullText, tt.input)
			}
			if got.Start < 0 || got.End > len(tt.input) || got.Start >= got.End {
				t.Fatalf("invalid span start=%d end=%d input_len=%d", got.Start, got.End, len(tt.input))
			}
			if got.FullText != tt.input[got.Start:got.End] {
				t.Fatalf("span text=%q, FullText=%q", tt.input[got.Start:got.End], got.FullText)
			}
			if got.Placeholder != rl.Masking.Placeholder {
				t.Fatalf("Placeholder=%q, want %q", got.Placeholder, rl.Masking.Placeholder)
			}
			if got.DataType != rl.DataType {
				t.Fatalf("DataType=%d, want %d", got.DataType, rl.DataType)
			}
			assertNoCapturedBoundaryLeak(t, rl, got.FullText)
		})
	}
}

func TestGenConfigRuleCasesCasesCoverEveryLoadedRule(t *testing.T) {
	t.Parallel()

	_, rulesByID := loadRealConfigScanner(t)
	if len(rulesByID) != expectedGenConfigRules {
		t.Fatalf("len(rulesByID)=%d, want %d", len(rulesByID), expectedGenConfigRules)
	}

	cases := genConfigRuleCases(t, rulesByID)
	covered := make(map[string]int, len(cases))
	for _, tt := range cases {
		if _, ok := rulesByID[tt.ruleID]; !ok {
			t.Fatalf("case %q references unknown rule %q", tt.name, tt.ruleID)
		}
		covered[tt.ruleID]++
	}

	var missing []string
	for id := range rulesByID {
		if covered[id] == 0 {
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("missing cases for rule IDs: %s", strings.Join(missing, ", "))
	}
}

func TestRealConfigCaptureGroupDecisionsAreExplicit(t *testing.T) {
	t.Parallel()

	_, rulesByID := loadRealConfigScanner(t)

	fullMatchWithHelperGroups := map[string]struct{}{
		"access_tokens.microsoft-teams-webhook.gl": {},
		"credentials.jwt-base64.gl":                {},
	}
	singleCaptureOfManyGroups := map[string]struct{}{
		"access_tokens.facebook-access-token.gl":    {},
		"access_tokens.sourcegraph-access-token.gl": {},
		"api_keys.dropbox-long-lived-api-token.gl":  {},
		"api_keys.heroku-api-key-v2.gl":             {},
		"api_keys.lob-api-key.gl":                   {},
		"api_keys.lob-pub-api-key.gl":               {},
		"api_keys.sonar-api-token.gl":               {},
	}
	multipleCaptureAlternatives := map[string]struct{}{
		"api_keys.atlassian-api-token.gl":       {},
		"credentials.password":                  {},
		"credentials.curl-auth-header.gl":       {},
		"credentials.curl-auth-user.gl":         {},
		"credentials.kubernetes-secret-yaml.gl": {},
	}

	for _, rl := range rulesByID {
		re, err := regexp.Compile("(?m)" + rl.Regex)
		if err != nil {
			t.Fatalf("compile %s: %v", rl.ID, err)
		}

		switch {
		case len(rl.Masking.CaptureGroups) == 0 && re.NumSubexp() > 0:
			if _, ok := fullMatchWithHelperGroups[rl.ID]; !ok {
				t.Fatalf("%s is full-match with %d regex groups but has no explicit helper-group decision", rl.ID, re.NumSubexp())
			}
		case len(rl.Masking.CaptureGroups) == 1 && re.NumSubexp() > 1:
			if _, ok := singleCaptureOfManyGroups[rl.ID]; !ok {
				t.Fatalf("%s selects one capture group out of %d but has no explicit decision", rl.ID, re.NumSubexp())
			}
		case len(rl.Masking.CaptureGroups) > 1:
			if _, ok := multipleCaptureAlternatives[rl.ID]; !ok {
				t.Fatalf("%s has multiple capture groups %v but has no explicit alternative decision", rl.ID, rl.Masking.CaptureGroups)
			}
		}
	}
}

func genConfigRuleCases(t *testing.T, rulesByID map[string]rule.Rule) []genConfigRuleCase {
	t.Helper()

	multi := multiCaptureCases()
	multiRuleIDs := make(map[string]struct{})
	for _, tt := range multi {
		multiRuleIDs[tt.ruleID] = struct{}{}
	}

	ids := make([]string, 0, len(rulesByID))
	for id := range rulesByID {
		if _, ok := multiRuleIDs[id]; !ok {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)

	cases := make([]genConfigRuleCase, 0, len(ids)+len(multi))
	for _, id := range ids {
		cases = append(cases, generatedCaseForRule(t, rulesByID[id]))
	}
	cases = append(cases, multi...)
	return cases
}

func multiCaptureCases() []genConfigRuleCase {
	atlassianToken := "aaaaaaaaaaaaaaaaaaaabbbb"
	atlassianStandalone := "ATATT3" + strings.Repeat("A", 186)

	return []genConfigRuleCase{
		{
			name:         "credentials.curl-auth-header.gl/basic-double",
			ruleID:       "credentials.curl-auth-header.gl",
			input:        `curl -H "Authorization: Basic dGVzdGluZw==" https://example.test `,
			wantFullText: "dGVzdGluZw==",
		},
		{
			name:         "credentials.curl-auth-header.gl/bearer-double",
			ruleID:       "credentials.curl-auth-header.gl",
			input:        `curl -H "Authorization: Bearer abcdefgh" https://example.test `,
			wantFullText: "abcdefgh",
		},
		{
			name:         "credentials.curl-auth-header.gl/raw-authorization-double",
			ruleID:       "credentials.curl-auth-header.gl",
			input:        `curl -H "Authorization: abcdefgh" https://example.test `,
			wantFullText: "abcdefgh",
		},
		{
			name:         "credentials.curl-auth-header.gl/x-api-key-double",
			ruleID:       "credentials.curl-auth-header.gl",
			input:        `curl -H "X-Api-Key: abcdefgh" https://example.test `,
			wantFullText: "abcdefgh",
		},
		{
			name:         "credentials.curl-auth-header.gl/basic-single",
			ruleID:       "credentials.curl-auth-header.gl",
			input:        `curl -H 'Authorization: Basic dGVzdGluZw==' https://example.test `,
			wantFullText: "dGVzdGluZw==",
		},
		{
			name:         "credentials.curl-auth-header.gl/bearer-single",
			ruleID:       "credentials.curl-auth-header.gl",
			input:        `curl -H 'Authorization: Bearer abcdefgh' https://example.test `,
			wantFullText: "abcdefgh",
		},
		{
			name:         "credentials.curl-auth-header.gl/raw-authorization-single",
			ruleID:       "credentials.curl-auth-header.gl",
			input:        `curl -H 'Authorization: abcdefgh' https://example.test `,
			wantFullText: "abcdefgh",
		},
		{
			name:         "credentials.curl-auth-header.gl/x-api-key-single",
			ruleID:       "credentials.curl-auth-header.gl",
			input:        `curl -H 'X-Api-Key: abcdefgh' https://example.test `,
			wantFullText: "abcdefgh",
		},
		{
			name:         "credentials.curl-auth-user.gl/double-quoted",
			ruleID:       "credentials.curl-auth-user.gl",
			input:        `curl -u "user:password" https://example.test `,
			wantFullText: "user:password",
		},
		{
			name:         "credentials.curl-auth-user.gl/single-quoted",
			ruleID:       "credentials.curl-auth-user.gl",
			input:        `curl -u 'user:password' https://example.test `,
			wantFullText: "user:password",
		},
		{
			name:         "credentials.curl-auth-user.gl/unquoted",
			ruleID:       "credentials.curl-auth-user.gl",
			input:        `curl -u user:password https://example.test `,
			wantFullText: "user:password",
		},
		{
			name:         "credentials.kubernetes-secret-yaml.gl/kind-before-data",
			ruleID:       "credentials.kubernetes-secret-yaml.gl",
			input:        "apiVersion: v1\nkind: Secret\nmetadata:\n  name: demo\ndata:\n  token: YWJjZGVmZ2hpag==\n",
			wantFullText: "YWJjZGVmZ2hpag==",
		},
		{
			name:         "credentials.kubernetes-secret-yaml.gl/data-before-kind",
			ruleID:       "credentials.kubernetes-secret-yaml.gl",
			input:        "apiVersion: v1\ndata:\n  password: YWJjZGVmZ2hpag==\nkind: Secret\nmetadata:\n  name: demo\n",
			wantFullText: "YWJjZGVmZ2hpag==",
		},
		{
			name:         "api_keys.atlassian-api-token.gl/assignment",
			ruleID:       "api_keys.atlassian-api-token.gl",
			input:        "jira_token = \"" + atlassianToken + "\";",
			wantFullText: atlassianToken,
		},
		{
			name:         "api_keys.atlassian-api-token.gl/standalone",
			ruleID:       "api_keys.atlassian-api-token.gl",
			input:        atlassianStandalone + ";",
			wantFullText: atlassianStandalone,
		},
	}
}

func generatedCaseForRule(t *testing.T, rl rule.Rule) genConfigRuleCase {
	t.Helper()

	input, wantFullText := generatedInputAndWant(t, rl)
	return genConfigRuleCase{
		name:         rl.ID,
		ruleID:       rl.ID,
		input:        input,
		wantFullText: wantFullText,
	}
}

func generatedInputAndWant(t *testing.T, rl rule.Rule) (string, string) {
	t.Helper()

	if input, want, ok := fixedValidatorCase(rl.ID); ok {
		return input, want
	}

	re, err := regexp.Compile("(?m)" + rl.Regex)
	if err != nil {
		t.Fatalf("compile %s: %v", rl.ID, err)
	}

	parsed, err := syntax.Parse("(?m)"+rl.Regex, syntax.Perl)
	if err != nil {
		t.Fatalf("parse %s: %v", rl.ID, err)
	}
	input := regexpExample(parsed.Simplify())

	loc := re.FindStringSubmatchIndex(input)
	if loc == nil {
		t.Fatalf("generated input does not match %s: input=%q regex=%q", rl.ID, input, rl.Regex)
	}

	if len(rl.Masking.CaptureGroups) == 0 {
		return input, input[loc[0]:loc[1]]
	}

	group := rl.Masking.CaptureGroups[0]
	groupIdx := group * 2
	if groupIdx+1 >= len(loc) || loc[groupIdx] < 0 || loc[groupIdx+1] <= loc[groupIdx] {
		t.Fatalf("generated input did not match capture group %d for %s: input=%q loc=%v", group, rl.ID, input, loc)
	}
	return input, input[loc[groupIdx]:loc[groupIdx+1]]
}

func fixedValidatorCase(ruleID string) (input string, wantFullText string, ok bool) {
	switch ruleID {
	case "ip-addrs.ipv4":
		return "8.8.8.8", "8.8.8.8", true
	case "ip-addrs.ipv4-cidr":
		return "8.8.8.0/24", "8.8.8.0/24", true
	case "ip-addrs.ipv4-public":
		return "8.8.4.4", "8.8.4.4", true
	case "ip-addrs.ipv4-private":
		return "10.1.2.3", "10.1.2.3", true
	case "ip-addrs.ipv6":
		return "2001:4860:4860:0000:0000:0000:0000:8888", "2001:4860:4860:0000:0000:0000:0000:8888", true
	case "ip-addrs.ipv6-cidr":
		return "2001:4860:4860:0000:0000:0000:0000:8888/64", "2001:4860:4860:0000:0000:0000:0000:8888/64", true
	case "ip-addrs.ipv6-public":
		return "2001:4860:4860:0000:0000:0000:0000:8844", "2001:4860:4860:0000:0000:0000:0000:8844", true
	case "ip-addrs.ipv6-private":
		return "fd00:0000:0000:0000:0000:0000:0000:0001", "fd00:0000:0000:0000:0000:0000:0000:0001", true
	case "pii.email":
		return "person.name+test@example.com", "person.name+test@example.com", true
	case "pii.phone-ru":
		// Built from split literals so the on-disk source has no contiguous phone.
		phone := "+7" + "999" + "123" + "45" + "67"
		return phone, phone, true
	case "pii.docs.snils":
		return "112-233-445 95", "112-233-445 95", true
	case "pii.docs.passport":
		return "паспорт 4509 123456", "4509 123456", true
	case "pii.docs.inn-person":
		return "500100732259", "500100732259", true
	case "pii.docs.inn-org":
		return "7707083893", "7707083893", true
	case "pii.docs.ogrn":
		return "1027700132195", "1027700132195", true
	case "pii.docs.ogrnip":
		return "304500116000157", "304500116000157", true
	case "pii.fin.credit-card":
		return "4111 1111 1111 1111", "4111 1111 1111 1111", true
	case "pii.fin.iban":
		return "GB82 WEST 1234 5698 7654 32", "GB82 WEST 1234 5698 7654 32", true
	default:
		return "", "", false
	}
}

func regexpExample(re *syntax.Regexp) string {
	switch re.Op {
	case syntax.OpNoMatch:
		panic("cannot generate example for no-match regexp")
	case syntax.OpEmptyMatch,
		syntax.OpBeginLine,
		syntax.OpEndLine,
		syntax.OpBeginText,
		syntax.OpEndText,
		syntax.OpWordBoundary,
		syntax.OpNoWordBoundary:
		return ""
	case syntax.OpLiteral:
		return string(re.Rune)
	case syntax.OpCharClass:
		return string(chooseRuneFromClass(re.Rune))
	case syntax.OpAnyCharNotNL, syntax.OpAnyChar:
		return "x"
	case syntax.OpCapture:
		return regexpExample(re.Sub[0])
	case syntax.OpStar:
		return ""
	case syntax.OpPlus:
		return regexpExample(re.Sub[0])
	case syntax.OpQuest:
		return regexpExample(re.Sub[0])
	case syntax.OpRepeat:
		var b strings.Builder
		n := re.Min
		if n == 0 && re.Max != 0 {
			n = 1
		}
		for range n {
			b.WriteString(regexpExample(re.Sub[0]))
		}
		return b.String()
	case syntax.OpConcat:
		var b strings.Builder
		for _, sub := range re.Sub {
			b.WriteString(regexpExample(sub))
		}
		return b.String()
	case syntax.OpAlternate:
		return regexpExample(re.Sub[0])
	default:
		panic(fmt.Sprintf("unsupported regexp op %s", re.Op))
	}
}

func chooseRuneFromClass(ranges []rune) rune {
	preferred := "aA1xX0_bBzZ-/+.=@:; \n"
	for _, candidate := range preferred {
		if runeInClass(candidate, ranges) {
			return candidate
		}
	}
	for r := rune(0); r <= utf8.RuneSelf; r++ {
		if runeInClass(r, ranges) {
			return r
		}
	}
	panic(fmt.Sprintf("no ASCII rune found for char class %v", ranges))
}
