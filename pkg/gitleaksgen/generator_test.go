package gitleaksgen

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	tomlsrc "github.com/cloud-ru-tech/guardrails-llm-filter/pkg/gitleaksgen/toml"
)

func TestBuildFMGuardrailsRegexRulesFromConfig_CaptureGroups(t *testing.T) {
	t.Parallel()

	cfg := tomlsrc.Config{
		Rules: []tomlsrc.Rule{
			{
				ID:    "single-group-api-key",
				Regex: `prefix-([a-z0-9]{8})(?:\s|$)`,
			},
			{
				ID:          "sonar-api-token",
				Regex:       `(?i)sonar(login|token)=((?:squ_)?[a-z0-9]{40})`,
				SecretGroup: 2,
			},
			{
				ID:    "atlassian-api-token",
				Regex: `([a-z0-9]{24})|(ATATT3[A-Za-z0-9_\-=]{186})`,
			},
			{
				ID:    "jwt-base64",
				Regex: `\bZXlK(?:(?P<alg>aGJHY2lPaU))[a-zA-Z0-9]{40,}`,
			},
			{
				ID:    "sentry-org-token",
				Regex: `unused`,
			},
			{
				ID:    "hashicorp-tf-password",
				Regex: `unused`,
			},
			{
				ID:    "curl-auth-user",
				Regex: `unused`,
			},
			{
				ID:    "jwt",
				Regex: `unused`,
			},
			{
				ID:    "gitlab-session-cookie",
				Regex: `unused`,
			},
			{
				ID:    "kubernetes-secret-yaml",
				Regex: `unused`,
			},
			{
				ID:    "sidekiq-sensitive-url",
				Regex: `unused`,
			},
		},
	}

	out, _, err := buildFMGuardrailsRegexRulesFromConfig(cfg, DefaultGitleaksPolicy())
	if err != nil {
		t.Fatalf("buildFMGuardrailsRegexRulesFromConfig error: %v", err)
	}

	assertCaptureGroups(t, out, "api_keys.single-group-api-key.gl", []int{1})
	assertCaptureGroups(t, out, "api_keys.sonar-api-token.gl", []int{2})
	assertCaptureGroups(t, out, "api_keys.atlassian-api-token.gl", []int{1, 2})
	assertCaptureGroups(t, out, "credentials.jwt-base64.gl", nil)

	sentry := findOutputRule(t, out, "access_tokens.sentry-org-token.gl")
	assertCaptureGroups(t, out, sentry.RuleID, []int{1})
	if sentry.Regex != gitleaksRegexOverrides["sentry-org-token"] {
		t.Fatalf("sentry regex=%q, want override %q", sentry.Regex, gitleaksRegexOverrides["sentry-org-token"])
	}

	hashicorp := findOutputRule(t, out, "credentials.hashicorp-tf-password.gl")
	assertCaptureGroups(t, out, hashicorp.RuleID, []int{1})
	if hashicorp.Regex != expectedNormalizedPromptBoundary(gitleaksRegexOverrides["hashicorp-tf-password"]) {
		t.Fatalf("hashicorp regex=%q, want normalized override %q", hashicorp.Regex, expectedNormalizedPromptBoundary(gitleaksRegexOverrides["hashicorp-tf-password"]))
	}

	curlAuthUser := findOutputRule(t, out, "credentials.curl-auth-user.gl")
	assertCaptureGroups(t, out, curlAuthUser.RuleID, []int{2, 3, 4})
	if curlAuthUser.Regex != gitleaksRegexOverrides["curl-auth-user"] {
		t.Fatalf("curl auth user regex=%q, want override %q", curlAuthUser.Regex, gitleaksRegexOverrides["curl-auth-user"])
	}
	assertRegexGroupMatches(t, curlAuthUser.Regex, `curl -u "api-user:secretPass123", https://example.test`, 2, "api-user:secretPass123")

	jwt := findOutputRule(t, out, "credentials.jwt.gl")
	assertCaptureGroups(t, out, jwt.RuleID, []int{1})
	if jwt.Regex != expectedNormalizedPromptBoundary(gitleaksRegexOverrides["jwt"]) {
		t.Fatalf("jwt regex=%q, want normalized override %q", jwt.Regex, expectedNormalizedPromptBoundary(gitleaksRegexOverrides["jwt"]))
	}

	gitlabSession := findOutputRule(t, out, "credentials.gitlab-session-cookie.gl")
	assertCaptureGroups(t, out, gitlabSession.RuleID, []int{1})
	if gitlabSession.Regex != gitleaksRegexOverrides["gitlab-session-cookie"] {
		t.Fatalf("gitlab session regex=%q, want override %q", gitlabSession.Regex, gitleaksRegexOverrides["gitlab-session-cookie"])
	}

	kubernetesSecret := findOutputRule(t, out, "credentials.kubernetes-secret-yaml.gl")
	assertCaptureGroups(t, out, kubernetesSecret.RuleID, []int{1, 2})
	if kubernetesSecret.Regex != gitleaksRegexOverrides["kubernetes-secret-yaml"] {
		t.Fatalf("kubernetes secret regex=%q, want override %q", kubernetesSecret.Regex, gitleaksRegexOverrides["kubernetes-secret-yaml"])
	}

	sidekiqURL := findOutputRule(t, out, "credentials.sidekiq-sensitive-url.gl")
	assertCaptureGroups(t, out, sidekiqURL.RuleID, []int{1})
	if sidekiqURL.Regex != gitleaksRegexOverrides["sidekiq-sensitive-url"] {
		t.Fatalf("sidekiq URL regex=%q, want override %q", sidekiqURL.Regex, gitleaksRegexOverrides["sidekiq-sensitive-url"])
	}
}

func TestBuildFMGuardrailsRegexRulesFromConfig_NormalizesStandardGitleaksBoundary(t *testing.T) {
	t.Parallel()

	cfg := tomlsrc.Config{
		Rules: []tomlsrc.Rule{
			{
				ID:    "capture-token",
				Regex: `token=([a-z0-9]{8})(?:[\x60'"\s;]|\\[nr]|$)`,
			},
			{
				ID:    "full-match-token",
				Regex: `token=[a-z0-9]{8}(?:[\x60'"\s;]|\\[nr]|$)`,
			},
		},
	}

	out, _, err := buildFMGuardrailsRegexRulesFromConfig(cfg, DefaultGitleaksPolicy())
	if err != nil {
		t.Fatalf("buildFMGuardrailsRegexRulesFromConfig error: %v", err)
	}

	capture := findOutputRule(t, out, "access_tokens.capture-token.gl")
	if capture.Regex != `token=([a-z0-9]{8})(?:[\x60'"\s,;:!?()\[\]{}]|\\[nr]|$)` {
		t.Fatalf("capture regex=%q", capture.Regex)
	}
	assertCaptureGroups(t, out, capture.RuleID, []int{1})

	fullMatch := findOutputRule(t, out, "access_tokens.full-match-token.gl")
	if fullMatch.Regex != `token=[a-z0-9]{8}(?:[\x60'"\s;]|\\[nr]|$)` {
		t.Fatalf("full match regex=%q", fullMatch.Regex)
	}
	assertCaptureGroups(t, out, fullMatch.RuleID, nil)
}

func assertCaptureGroups(t *testing.T, out OutputFile, ruleID string, want []int) {
	t.Helper()

	got := findOutputRule(t, out, ruleID).Masking.CaptureGroups
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s capture_groups=%v, want %v", ruleID, got, want)
	}
}

func assertRegexGroupMatches(t *testing.T, pattern string, input string, group int, want string) {
	t.Helper()

	re, err := regexp.Compile("(?m)" + pattern)
	if err != nil {
		t.Fatalf("compile regex: %v", err)
	}
	match := re.FindStringSubmatch(input)
	if match == nil {
		t.Fatalf("regex did not match input %q", input)
	}
	if len(match) <= group {
		t.Fatalf("regex has %d submatches, want group %d", len(match)-1, group)
	}
	if match[group] != want {
		t.Fatalf("group %d=%q, want %q", group, match[group], want)
	}
}

func expectedNormalizedPromptBoundary(pattern string) string {
	if !strings.HasSuffix(pattern, standardGitleaksBoundary) {
		return pattern
	}
	return strings.TrimSuffix(pattern, standardGitleaksBoundary) + promptTokenBoundary
}

func findOutputRule(t *testing.T, out OutputFile, ruleID string) OutputRule {
	t.Helper()

	for _, dataType := range out.DataTypes {
		for _, rl := range dataType.Rules {
			if rl.RuleID == ruleID {
				return rl
			}
		}
	}
	t.Fatalf("rule %q not found in output", ruleID)
	return OutputRule{}
}
