package rules_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/scanners/sensitive"
)

type realConfigRuleCase struct {
	name         string
	ruleID       string
	input        string
	wantFullText string
}

func TestRealConfigRuleCases_ScanSemanticSpan(t *testing.T) {
	t.Parallel()

	scanner, rulesByID := loadRealConfigScanner(t)
	cases := realConfigRuleCases()
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

func TestRealConfigRuleCases_CoverEveryLoadedRule(t *testing.T) {
	t.Parallel()

	_, rulesByID := loadRealConfigScanner(t)
	if len(rulesByID) != expectedGenConfigRules {
		t.Fatalf("len(rulesByID)=%d, want %d", len(rulesByID), expectedGenConfigRules)
	}

	cases := realConfigRuleCases()
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

func realConfigRuleCases() []realConfigRuleCase {
	return []realConfigRuleCase{
		{
			name:         "access_tokens.1password-secret-key.gl",
			ruleID:       "access_tokens.1password-secret-key.gl",
			input:        "1PASSWORD_SECRET_KEY=\"A3-AAAAAA-AAAAAAAAAAA-AAAAA-AAAAA-AAAAA\"",
			wantFullText: "A3-AAAAAA-AAAAAAAAAAA-AAAAA-AAAAA-AAAAA",
		},
		{
			name:         "access_tokens.1password-service-account-token.gl",
			ruleID:       "access_tokens.1password-service-account-token.gl",
			input:        "1PASSWORD_SERVICE_ACCOUNT_TOKEN=\"ops_eyJaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa===\"",
			wantFullText: "ops_eyJaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa===",
		},
		{
			name:         "access_tokens.age-secret-key.gl",
			ruleID:       "access_tokens.age-secret-key.gl",
			input:        "AGE_SECRET_KEY=\"AGE-SECRET-KEY-1AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\"",
			wantFullText: "AGE-SECRET-KEY-1AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		},
		{
			name:         "access_tokens.airtable-personnal-access-token.gl",
			ruleID:       "access_tokens.airtable-personnal-access-token.gl",
			input:        "airtable_personnal_access_token = \"pataaaaaaaaaaaaaa.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "pataaaaaaaaaaaaaa.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:   "access_tokens.alibaba-access-key-id.gl",
			ruleID: "access_tokens.alibaba-access-key-id.gl",
			// Built by concatenation so the synthetic key is not a contiguous
			// literal in source — secret scanners (GitHub push protection,
			// gitleaks) would otherwise flag the fixture as a real credential.
			input:        "alibaba_access_key_id = \"LTAI" + strings.Repeat("a", 20) + "\";",
			wantFullText: "LTAI" + strings.Repeat("a", 20),
		},
		{
			name:         "access_tokens.alibaba-secret-key.gl",
			ruleID:       "access_tokens.alibaba-secret-key.gl",
			input:        "alibaba_secret_key = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.artifactory-reference-token.gl",
			ruleID:       "access_tokens.artifactory-reference-token.gl",
			input:        "ARTIFACTORY_REFERENCE_TOKEN=\"cmVmdaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "cmVmdaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.authress-service-client-access-key.gl",
			ruleID:       "access_tokens.authress-service-client-access-key.gl",
			input:        "AUTHRESS_SERVICE_CLIENT_ACCESS_KEY=\"sc_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.aaaaaa.acc_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "sc_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.aaaaaa.acc_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.aws-access-token.gl",
			ruleID:       "access_tokens.aws-access-token.gl",
			input:        "AWS_ACCESS_TOKEN=\"A3TAAAAAAAAAAAAAAAAA\"",
			wantFullText: "A3TAAAAAAAAAAAAAAAAA",
		},
		{
			name:         "access_tokens.bittrex-access-key.gl",
			ruleID:       "access_tokens.bittrex-access-key.gl",
			input:        "bittrex_access_key = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.bittrex-secret-key.gl",
			ruleID:       "access_tokens.bittrex-secret-key.gl",
			input:        "bittrex_secret_key = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.cloudflare-origin-ca-key.gl",
			ruleID:       "access_tokens.cloudflare-origin-ca-key.gl",
			input:        "cloudflare_origin_ca_key = \"v1.0-aaaaaaaaaaaaaaaaaaaaaaaa-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "v1.0-aaaaaaaaaaaaaaaaaaaaaaaa-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.codecov-access-token.gl",
			ruleID:       "access_tokens.codecov-access-token.gl",
			input:        "codecov_access_token = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.coinbase-access-token.gl",
			ruleID:       "access_tokens.coinbase-access-token.gl",
			input:        "coinbase_access_token = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.confluent-access-token.gl",
			ruleID:       "access_tokens.confluent-access-token.gl",
			input:        "confluent_access_token = \"aaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.confluent-secret-key.gl",
			ruleID:       "access_tokens.confluent-secret-key.gl",
			input:        "confluent_secret_key = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.datadog-access-token.gl",
			ruleID:       "access_tokens.datadog-access-token.gl",
			input:        "datadog_access_token = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.digitalocean-access-token.gl",
			ruleID:       "access_tokens.digitalocean-access-token.gl",
			input:        "DIGITALOCEAN_ACCESS_TOKEN=\"doo_v1_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "doo_v1_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.digitalocean-pat.gl",
			ruleID:       "access_tokens.digitalocean-pat.gl",
			input:        "DIGITALOCEAN_PAT=\"dop_v1_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "dop_v1_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.droneci-access-token.gl",
			ruleID:       "access_tokens.droneci-access-token.gl",
			input:        "droneci_access_token = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.etsy-access-token.gl",
			ruleID:       "access_tokens.etsy-access-token.gl",
			input:        "etsy_access_token = \"aaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.facebook-access-token.gl",
			ruleID:       "access_tokens.facebook-access-token.gl",
			input:        "facebook_access_token = \"1111111111111111%aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "1111111111111111%aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.facebook-page-access-token.gl",
			ruleID:       "access_tokens.facebook-page-access-token.gl",
			input:        "facebook_page_access_token = \"EAACaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "EAACaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.finnhub-access-token.gl",
			ruleID:       "access_tokens.finnhub-access-token.gl",
			input:        "finnhub_access_token = \"aaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.flickr-access-token.gl",
			ruleID:       "access_tokens.flickr-access-token.gl",
			input:        "flickr_access_token = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.flutterwave-encryption-key.gl",
			ruleID:       "access_tokens.flutterwave-encryption-key.gl",
			input:        "FLUTTERWAVE_ENCRYPTION_KEY=\"FLWSECK_TEST-aaaaaaaaaaaa\"",
			wantFullText: "FLWSECK_TEST-aaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.flutterwave-public-key.gl",
			ruleID:       "access_tokens.flutterwave-public-key.gl",
			input:        "FLUTTERWAVE_PUBLIC_KEY=\"FLWPUBK_TEST-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-X\"",
			wantFullText: "FLWPUBK_TEST-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-X",
		},
		{
			name:         "access_tokens.flutterwave-secret-key.gl",
			ruleID:       "access_tokens.flutterwave-secret-key.gl",
			input:        "FLUTTERWAVE_SECRET_KEY=\"FLWSECK_TEST-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-X\"",
			wantFullText: "FLWSECK_TEST-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-X",
		},
		{
			name:         "access_tokens.freemius-secret-key.gl",
			ruleID:       "access_tokens.freemius-secret-key.gl",
			input:        "$config = ['secret_key' => 'SK_aaaaaaaaaaaaaaaaaaaaaaaaaaaaa'];",
			wantFullText: "SK_aaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.freshbooks-access-token.gl",
			ruleID:       "access_tokens.freshbooks-access-token.gl",
			input:        "freshbooks_access_token = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.github-app-token.gl",
			ruleID:       "access_tokens.github-app-token.gl",
			input:        "GITHUB_TOKEN=\"ghs_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "ghs_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.github-fine-grained-pat.gl",
			ruleID:       "access_tokens.github-fine-grained-pat.gl",
			input:        "GITHUB_TOKEN=\"github_pat_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "github_pat_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.github-pat.gl",
			ruleID:       "access_tokens.github-pat.gl",
			input:        "GITHUB_TOKEN=\"ghp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "ghp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.gitlab-cicd-job-token.gl",
			ruleID:       "access_tokens.gitlab-cicd-job-token.gl",
			input:        "GITLAB_TOKEN=\"glcbt-aaaaa_aaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "glcbt-aaaaa_aaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.gitlab-deploy-token.gl",
			ruleID:       "access_tokens.gitlab-deploy-token.gl",
			input:        "GITLAB_TOKEN=\"gldt-aaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "gldt-aaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.gitlab-feature-flag-client-token.gl",
			ruleID:       "access_tokens.gitlab-feature-flag-client-token.gl",
			input:        "GITLAB_TOKEN=\"glffct-aaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "glffct-aaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.gitlab-feed-token.gl",
			ruleID:       "access_tokens.gitlab-feed-token.gl",
			input:        "GITLAB_TOKEN=\"glft-aaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "glft-aaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.gitlab-incoming-mail-token.gl",
			ruleID:       "access_tokens.gitlab-incoming-mail-token.gl",
			input:        "GITLAB_TOKEN=\"glimt-aaaaaaaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "glimt-aaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.gitlab-kubernetes-agent-token.gl",
			ruleID:       "access_tokens.gitlab-kubernetes-agent-token.gl",
			input:        "GITLAB_TOKEN=\"glagent-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "glagent-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.gitlab-pat-routable.gl",
			ruleID:       "access_tokens.gitlab-pat-routable.gl",
			input:        "GITLAB_TOKEN=\"glpat-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.aaaaaaaaa\"",
			wantFullText: "glpat-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.aaaaaaaaa",
		},
		{
			name:         "access_tokens.gitlab-pat.gl",
			ruleID:       "access_tokens.gitlab-pat.gl",
			input:        "GITLAB_TOKEN=\"glpat-aaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "glpat-aaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.gitlab-ptt.gl",
			ruleID:       "access_tokens.gitlab-ptt.gl",
			input:        "GITLAB_TOKEN=\"glptt-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "glptt-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.gitlab-rrt.gl",
			ruleID:       "access_tokens.gitlab-rrt.gl",
			input:        "GITLAB_TOKEN=\"GR1348941aaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "GR1348941aaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.gitlab-runner-authentication-token-routable.gl",
			ruleID:       "access_tokens.gitlab-runner-authentication-token-routable.gl",
			input:        "GITLAB_TOKEN=\"glrt-t1_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.aaaaaaaaa\"",
			wantFullText: "glrt-t1_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.aaaaaaaaa",
		},
		{
			name:         "access_tokens.gitlab-runner-authentication-token.gl",
			ruleID:       "access_tokens.gitlab-runner-authentication-token.gl",
			input:        "GITLAB_TOKEN=\"glrt-aaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "glrt-aaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.gitlab-scim-token.gl",
			ruleID:       "access_tokens.gitlab-scim-token.gl",
			input:        "GITLAB_TOKEN=\"glsoat-aaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "glsoat-aaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.gitter-access-token.gl",
			ruleID:       "access_tokens.gitter-access-token.gl",
			input:        "gitter_access_token = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.grafana-service-account-token.gl",
			ruleID:       "access_tokens.grafana-service-account-token.gl",
			input:        "GRAFANA_SERVICE_ACCOUNT_TOKEN=\"GLSA_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa_aaaaaaaa\"",
			wantFullText: "GLSA_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa_aaaaaaaa",
		},
		{
			name:         "access_tokens.huggingface-access-token.gl",
			ruleID:       "access_tokens.huggingface-access-token.gl",
			input:        "HUGGINGFACE_ACCESS_TOKEN=\"hf_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "hf_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.jfrog-identity-token.gl",
			ruleID:       "access_tokens.jfrog-identity-token.gl",
			input:        "jfrog_identity_token = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.kraken-access-token.gl",
			ruleID:       "access_tokens.kraken-access-token.gl",
			input:        "kraken_access_token = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.kucoin-access-token.gl",
			ruleID:       "access_tokens.kucoin-access-token.gl",
			input:        "kucoin_access_token = \"aaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.kucoin-secret-key.gl",
			ruleID:       "access_tokens.kucoin-secret-key.gl",
			input:        "kucoin_secret_key = \"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.launchdarkly-access-token.gl",
			ruleID:       "access_tokens.launchdarkly-access-token.gl",
			input:        "launchdarkly_access_token = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.mailgun-pub-key.gl",
			ruleID:       "access_tokens.mailgun-pub-key.gl",
			input:        "mailgun_pub_key = \"PUBKEY-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "PUBKEY-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.mailgun-signing-key.gl",
			ruleID:       "access_tokens.mailgun-signing-key.gl",
			input:        "mailgun_signing_key = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-aaaaaaaa-aaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-aaaaaaaa-aaaaaaaa",
		},
		{
			name:         "access_tokens.mattermost-access-token.gl",
			ruleID:       "access_tokens.mattermost-access-token.gl",
			input:        "mattermost_access_token = \"aaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.maxmind-license-key.gl",
			ruleID:       "access_tokens.maxmind-license-key.gl",
			input:        "MAXMIND_LICENSE_KEY=\"aaaaaa_aaaaaaaaaaaaaaaaaaaaaaaaaaaaa_mmk\"",
			wantFullText: "aaaaaa_aaaaaaaaaaaaaaaaaaaaaaaaaaaaa_mmk",
		},
		{
			name:         "access_tokens.microsoft-teams-webhook.gl",
			ruleID:       "access_tokens.microsoft-teams-webhook.gl",
			input:        "teams_webhook_url = \"https://a.webhook.office.com/webhookb2/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa@aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa/IncomingWebhook/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa\"",
			wantFullText: "https://a.webhook.office.com/webhookb2/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa@aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa/IncomingWebhook/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.netlify-access-token.gl",
			ruleID:       "access_tokens.netlify-access-token.gl",
			input:        "netlify_access_token = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.new-relic-insert-key.gl",
			ruleID:       "access_tokens.new-relic-insert-key.gl",
			input:        "new_relic_insert_key = \"NRII-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "NRII-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.npm-access-token.gl",
			ruleID:       "access_tokens.npm-access-token.gl",
			input:        "NPM_ACCESS_TOKEN=\"NPM_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "NPM_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.npm-token",
			ruleID:       "access_tokens.npm-token",
			input:        "//registry.npmjs.org/:_authToken=npm_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			wantFullText: "npm_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.nytimes-access-token.gl",
			ruleID:       "access_tokens.nytimes-access-token.gl",
			input:        "nytimes_access_token = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.okta-access-token.gl",
			ruleID:       "access_tokens.okta-access-token.gl",
			input:        "okta_access_token = \"00aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "00aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.openshift-user-token.gl",
			ruleID:       "access_tokens.openshift-user-token.gl",
			input:        "OPENSHIFT_USER_TOKEN=\"sha256~aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "sha256~aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.plaid-secret-key.gl",
			ruleID:       "access_tokens.plaid-secret-key.gl",
			input:        "plaid_secret_key = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.private-key-pem",
			ruleID:       "access_tokens.private-key-pem",
			input:        "-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEAaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n-----END RSA PRIVATE KEY-----",
			wantFullText: "-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEAaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n-----END RSA PRIVATE KEY-----",
		},
		{
			name:         "access_tokens.private-key.gl",
			ruleID:       "access_tokens.private-key.gl",
			input:        "-----BEGIN OPENSSH PRIVATE KEY-----\naaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n-----END OPENSSH PRIVATE KEY-----",
			wantFullText: "-----BEGIN OPENSSH PRIVATE KEY-----\naaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n-----END OPENSSH PRIVATE KEY-----",
		},
		{
			name:         "access_tokens.pypi-upload-token.gl",
			ruleID:       "access_tokens.pypi-upload-token.gl",
			input:        "PYPI_UPLOAD_TOKEN=\"pypi-AgEIcHlwaS5vcmcaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "pypi-AgEIcHlwaS5vcmcaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.rapidapi-access-token.gl",
			ruleID:       "access_tokens.rapidapi-access-token.gl",
			input:        "rapidapi_access_token = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.sendbird-access-token.gl",
			ruleID:       "access_tokens.sendbird-access-token.gl",
			input:        "sendbird_access_token = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.sentry-access-token.gl",
			ruleID:       "access_tokens.sentry-access-token.gl",
			input:        "sentry_access_token = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.sentry-org-token.gl",
			ruleID:       "access_tokens.sentry-org-token.gl",
			input:        "sentry_org_token = \"sntrys_eyJpYXQiOaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaLCJyZWdpb25fdXJsaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa==_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "sntrys_eyJpYXQiOaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaLCJyZWdpb25fdXJsaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa==_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.sentry-user-token.gl",
			ruleID:       "access_tokens.sentry-user-token.gl",
			input:        "sentry_user_token = \"sntryu_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "sntryu_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.settlemint-application-access-token.gl",
			ruleID:       "access_tokens.settlemint-application-access-token.gl",
			input:        "SETTLEMINT_APPLICATION_ACCESS_TOKEN=\"sm_aat_aaaaaaaaaaaaaaaa\"",
			wantFullText: "sm_aat_aaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.settlemint-personal-access-token.gl",
			ruleID:       "access_tokens.settlemint-personal-access-token.gl",
			input:        "SETTLEMINT_PERSONAL_ACCESS_TOKEN=\"sm_pat_aaaaaaaaaaaaaaaa\"",
			wantFullText: "sm_pat_aaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.settlemint-service-access-token.gl",
			ruleID:       "access_tokens.settlemint-service-access-token.gl",
			input:        "SETTLEMINT_SERVICE_ACCESS_TOKEN=\"sm_sat_aaaaaaaaaaaaaaaa\"",
			wantFullText: "sm_sat_aaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.shopify-access-token.gl",
			ruleID:       "access_tokens.shopify-access-token.gl",
			input:        "SHOPIFY_ACCESS_TOKEN=\"shpat_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "shpat_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.shopify-custom-access-token.gl",
			ruleID:       "access_tokens.shopify-custom-access-token.gl",
			input:        "SHOPIFY_CUSTOM_ACCESS_TOKEN=\"shpca_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "shpca_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.shopify-private-app-access-token.gl",
			ruleID:       "access_tokens.shopify-private-app-access-token.gl",
			input:        "SHOPIFY_PRIVATE_APP_ACCESS_TOKEN=\"shppa_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "shppa_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.slack-app-token.gl",
			ruleID:       "access_tokens.slack-app-token.gl",
			input:        "SLACK_TOKEN=\"XAPP-1-a-1-a\"",
			wantFullText: "XAPP-1-a-1-a",
		},
		{
			name:         "access_tokens.slack-bot-token.gl",
			ruleID:       "access_tokens.slack-bot-token.gl",
			input:        "SLACK_TOKEN=\"xoxb-1111111111111-1111111111111\"",
			wantFullText: "xoxb-1111111111111-1111111111111",
		},
		{
			name:         "access_tokens.slack-config-access-token.gl",
			ruleID:       "access_tokens.slack-config-access-token.gl",
			input:        "SLACK_TOKEN=\"XOXExXOXb-1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "XOXExXOXb-1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.slack-legacy-bot-token.gl",
			ruleID:       "access_tokens.slack-legacy-bot-token.gl",
			input:        "SLACK_TOKEN=\"xoxb-11111111111111-aaaaaaaaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "xoxb-11111111111111-aaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.slack-legacy-token.gl",
			ruleID:       "access_tokens.slack-legacy-token.gl",
			input:        "SLACK_TOKEN=\"xoxo-1-1-1-a\"",
			wantFullText: "xoxo-1-1-1-a",
		},
		{
			name:         "access_tokens.slack-legacy-workspace-token.gl",
			ruleID:       "access_tokens.slack-legacy-workspace-token.gl",
			input:        "SLACK_TOKEN=\"xoxa-1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "xoxa-1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.slack-user-token.gl",
			ruleID:       "access_tokens.slack-user-token.gl",
			input:        "SLACK_TOKEN=\"xoxe-1111111111111-1111111111111-1111111111111-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "xoxe-1111111111111-1111111111111-1111111111111-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.slack-webhook-url.gl",
			ruleID:       "access_tokens.slack-webhook-url.gl",
			input:        "SLACK_TOKEN=\"https://hooksxslackxcom/services/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "https://hooksxslackxcom/services/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.sourcegraph-access-token.gl",
			ruleID:       "access_tokens.sourcegraph-access-token.gl",
			input:        "SOURCEGRAPH_ACCESS_TOKEN=\"SGP_aaaaaaaaaaaaaaaa_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "SGP_aaaaaaaaaaaaaaaa_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.square-access-token.gl",
			ruleID:       "access_tokens.square-access-token.gl",
			input:        "SQUARE_ACCESS_TOKEN=\"EAAAaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "EAAAaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.squarespace-access-token.gl",
			ruleID:       "access_tokens.squarespace-access-token.gl",
			input:        "squarespace_access_token = \"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.stripe-access-token.gl",
			ruleID:       "access_tokens.stripe-access-token.gl",
			input:        "STRIPE_ACCESS_TOKEN=\"sk_test_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "sk_test_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.sumologic-access-token.gl",
			ruleID:       "access_tokens.sumologic-access-token.gl",
			input:        "sumologic_access_token = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.travisci-access-token.gl",
			ruleID:       "access_tokens.travisci-access-token.gl",
			input:        "travisci_access_token = \"aaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.twilio-auth-token",
			ruleID:       "access_tokens.twilio-auth-token",
			input:        "twilio auth_token = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.twitter-access-token.gl",
			ruleID:       "access_tokens.twitter-access-token.gl",
			input:        "twitter_access_token = \"1111111111111111111111111-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "1111111111111111111111111-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.vault-batch-token.gl",
			ruleID:       "access_tokens.vault-batch-token.gl",
			input:        "VAULT_BATCH_TOKEN=\"hvb.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "hvb.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.vault-service-token.gl",
			ruleID:       "access_tokens.vault-service-token.gl",
			input:        "VAULT_SERVICE_TOKEN=\"hvs.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "hvs.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.vault-token",
			ruleID:       "access_tokens.vault-token",
			input:        "VAULT_TOKEN=\"hvb.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "hvb.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.yandex-access-token.gl",
			ruleID:       "access_tokens.yandex-access-token.gl",
			input:        "yandex_access_token = \"T1.a==.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa==\";",
			wantFullText: "T1.a==.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa==",
		},
		{
			name:         "access_tokens.yandex-aws-access-token.gl",
			ruleID:       "access_tokens.yandex-aws-access-token.gl",
			input:        "yandex_aws_access_token = \"YCaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "YCaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "access_tokens.zendesk-secret-key.gl",
			ruleID:       "access_tokens.zendesk-secret-key.gl",
			input:        "zendesk_secret_key = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.adafruit-api-key.gl",
			ruleID:       "api_keys.adafruit-api-key.gl",
			input:        "adafruit_api_key = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.airtable-api-key.gl",
			ruleID:       "api_keys.airtable-api-key.gl",
			input:        "airtable_api_key = \"aaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.algolia-api-key.gl",
			ruleID:       "api_keys.algolia-api-key.gl",
			input:        "algolia_api_key = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.anthropic-admin-api-key.gl",
			ruleID:       "api_keys.anthropic-admin-api-key.gl",
			input:        "const anthropic_admin_api_key = \"sk-ant-admin01-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaAA\";",
			wantFullText: "sk-ant-admin01-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaAA",
		},
		{
			name:         "api_keys.anthropic-api-key.gl",
			ruleID:       "api_keys.anthropic-api-key.gl",
			input:        "const anthropic_api_key = \"sk-ant-api03-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaAA\";",
			wantFullText: "sk-ant-api03-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaAA",
		},
		{
			name:         "api_keys.artifactory-api-key.gl",
			ruleID:       "api_keys.artifactory-api-key.gl",
			input:        "const artifactory_api_key = \"AKCpaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "AKCpaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.aws-amazon-bedrock-api-key-long-lived.gl",
			ruleID:       "api_keys.aws-amazon-bedrock-api-key-long-lived.gl",
			input:        "const aws_amazon_bedrock_api_key_long_lived = \"ABSKaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa==\";",
			wantFullText: "ABSKaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa==",
		},
		{
			name:         "api_keys.aws-amazon-bedrock-api-key-short-lived.gl",
			ruleID:       "api_keys.aws-amazon-bedrock-api-key-short-lived.gl",
			input:        "const aws_amazon_bedrock_api_key_short_lived = \"bedrock-api-key-YmVkcm9jay5hbWF6b25hd3MuY29t\";",
			wantFullText: "bedrock-api-key-YmVkcm9jay5hbWF6b25hd3MuY29t",
		},
		{
			name:         "api_keys.beamer-api-token.gl",
			ruleID:       "api_keys.beamer-api-token.gl",
			input:        "beamer_api_token = \"B_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "B_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.cisco-meraki-api-key.gl",
			ruleID:       "api_keys.cisco-meraki-api-key.gl",
			input:        "cisco_meraki_api_key = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.clickhouse-cloud-api-secret-key.gl",
			ruleID:       "api_keys.clickhouse-cloud-api-secret-key.gl",
			input:        "const clickhouse_cloud_api_secret_key = \"4b1daaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "4b1daaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.clojars-api-token.gl",
			ruleID:       "api_keys.clojars-api-token.gl",
			input:        "const clojars_api_token = \"CLOJARS_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "CLOJARS_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.cloudflare-api-key.gl",
			ruleID:       "api_keys.cloudflare-api-key.gl",
			input:        "cloudflare_api_key = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.cloudflare-global-api-key.gl",
			ruleID:       "api_keys.cloudflare-global-api-key.gl",
			input:        "cloudflare_global_api_key = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.cohere-api-token.gl",
			ruleID:       "api_keys.cohere-api-token.gl",
			input:        "cohere_api_token = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.contentful-delivery-api-token.gl",
			ruleID:       "api_keys.contentful-delivery-api-token.gl",
			input:        "contentful_delivery_api_token = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.databricks-api-token.gl",
			ruleID:       "api_keys.databricks-api-token.gl",
			input:        "const databricks_api_token = \"dapiaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-1\";",
			wantFullText: "dapiaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-1",
		},
		{
			name:         "api_keys.defined-networking-api-token.gl",
			ruleID:       "api_keys.defined-networking-api-token.gl",
			input:        "dnkey = \"DNKEY-aaaaaaaaaaaaaaaaaaaaaaaaaa-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "DNKEY-aaaaaaaaaaaaaaaaaaaaaaaaaa-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.discord-api-token.gl",
			ruleID:       "api_keys.discord-api-token.gl",
			input:        "discord_api_token = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.doppler-api-token.gl",
			ruleID:       "api_keys.doppler-api-token.gl",
			input:        "const doppler_api_token = \"dp.pt.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "dp.pt.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.dropbox-api-token.gl",
			ruleID:       "api_keys.dropbox-api-token.gl",
			input:        "dropbox_api_token = \"aaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.dropbox-long-lived-api-token.gl",
			ruleID:       "api_keys.dropbox-long-lived-api-token.gl",
			input:        "dropbox_token = \"aaaaaaaaaaaAAAAAAAAAAaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaAAAAAAAAAAaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.dropbox-short-lived-api-token.gl",
			ruleID:       "api_keys.dropbox-short-lived-api-token.gl",
			input:        "dropbox_token = \"SL.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "SL.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.duffel-api-token.gl",
			ruleID:       "api_keys.duffel-api-token.gl",
			input:        "const duffel_api_token = \"duffel_test_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "duffel_test_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.dynatrace-api-token.gl",
			ruleID:       "api_keys.dynatrace-api-token.gl",
			input:        "const dynatrace_api_token = \"dt0c01.aaaaaaaaaaaaaaaaaaaaaaaa.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "dt0c01.aaaaaaaaaaaaaaaaaaaaaaaa.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.easypost-api-token.gl",
			ruleID:       "api_keys.easypost-api-token.gl",
			input:        "const easypost_api_token = \"EZAKaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "EZAKaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.easypost-test-api-token.gl",
			ruleID:       "api_keys.easypost-test-api-token.gl",
			input:        "const easypost_test_api_token = \"EZTKaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "EZTKaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.fastly-api-token.gl",
			ruleID:       "api_keys.fastly-api-token.gl",
			input:        "fastly_api_token = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.finicity-api-token.gl",
			ruleID:       "api_keys.finicity-api-token.gl",
			input:        "finicity_api_token = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.flyio-access-token.gl",
			ruleID:       "api_keys.flyio-access-token.gl",
			input:        "const flyio_access_token = \"fo1_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "fo1_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.frameio-api-token.gl",
			ruleID:       "api_keys.frameio-api-token.gl",
			input:        "const frameio_api_token = \"fio-u-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "fio-u-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.gcp-api-key.gl",
			ruleID:       "api_keys.gcp-api-key.gl",
			input:        "const gcp_api_key = \"AIzaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "AIzaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.gocardless-api-token.gl",
			ruleID:       "api_keys.gocardless-api-token.gl",
			input:        "gocardless_api_token = \"LIVE_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "LIVE_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.grafana-api-key.gl",
			ruleID:       "api_keys.grafana-api-key.gl",
			input:        "const grafana_api_key = \"EYJRIJOIaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa===\";",
			wantFullText: "EYJRIJOIaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa===",
		},
		{
			name:         "api_keys.grafana-cloud-api-token.gl",
			ruleID:       "api_keys.grafana-cloud-api-token.gl",
			input:        "const grafana_cloud_api_token = \"GLC_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa===\";",
			wantFullText: "GLC_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa===",
		},
		{
			name:         "api_keys.harness-api-key.gl",
			ruleID:       "api_keys.harness-api-key.gl",
			input:        "const harness_api_key = \"pat.aaaaaaaaaaaaaaaaaaaaaa.aaaaaaaaaaaaaaaaaaaaaaaa.aaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "pat.aaaaaaaaaaaaaaaaaaaaaa.aaaaaaaaaaaaaaaaaaaaaaaa.aaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.hashicorp-tf-api-token.gl",
			ruleID:       "api_keys.hashicorp-tf-api-token.gl",
			input:        "const hashicorp_tf_api_token = \"aaaaaaaaaaaaaa.atlasv1.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaa.atlasv1.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.heroku-api-key-v2.gl",
			ruleID:       "api_keys.heroku-api-key-v2.gl",
			input:        "heroku_api_key_v2 = \"HRKU-AAaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "HRKU-AAaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.heroku-api-key.gl",
			ruleID:       "api_keys.heroku-api-key.gl",
			input:        "heroku_api_key = \"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		},
		{
			name:         "api_keys.hubspot-api-key.gl",
			ruleID:       "api_keys.hubspot-api-key.gl",
			input:        "hubspot_api_key = \"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		},
		{
			name:         "api_keys.huggingface-organization-api-token.gl",
			ruleID:       "api_keys.huggingface-organization-api-token.gl",
			input:        "const huggingface_organization_api_token = \"api_org_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "api_org_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.infracost-api-token.gl",
			ruleID:       "api_keys.infracost-api-token.gl",
			input:        "const infracost_api_token = \"ico-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "ico-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.intercom-api-key.gl",
			ruleID:       "api_keys.intercom-api-key.gl",
			input:        "intercom_api_key = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.jfrog-api-key.gl",
			ruleID:       "api_keys.jfrog-api-key.gl",
			input:        "jfrog_api_key = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.linear-api-key.gl",
			ruleID:       "api_keys.linear-api-key.gl",
			input:        "linear_api_key = \"lin_api_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "lin_api_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.lob-api-key.gl",
			ruleID:       "api_keys.lob-api-key.gl",
			input:        "lob_api_key = \"LIVE_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "LIVE_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.lob-pub-api-key.gl",
			ruleID:       "api_keys.lob-pub-api-key.gl",
			input:        "lob_pub_api_key = \"TEST_PUB_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "TEST_PUB_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.mailchimp-api-key.gl",
			ruleID:       "api_keys.mailchimp-api-key.gl",
			input:        "mailchimp_api_key = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-US11\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-US11",
		},
		{
			name:         "api_keys.mailgun-private-api-token.gl",
			ruleID:       "api_keys.mailgun-private-api-token.gl",
			input:        "mailgun_private_api_token = \"KEY-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "KEY-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.mapbox-api-token.gl",
			ruleID:       "api_keys.mapbox-api-token.gl",
			input:        "mapbox_api_token = \"PK.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.aaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "PK.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.aaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.messagebird-api-token.gl",
			ruleID:       "api_keys.messagebird-api-token.gl",
			input:        "messagebird_api_token = \"aaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.new-relic-browser-api-token.gl",
			ruleID:       "api_keys.new-relic-browser-api-token.gl",
			input:        "new_relic_browser_api_token = \"NRJS-aaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "NRJS-aaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.new-relic-user-api-key.gl",
			ruleID:       "api_keys.new-relic-user-api-key.gl",
			input:        "new_relic_user_api_key = \"NRAK-aaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "NRAK-aaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.notion-api-token.gl",
			ruleID:       "api_keys.notion-api-token.gl",
			input:        "const notion_api_token = \"ntn_11111111111aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "ntn_11111111111aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.octopus-deploy-api-key.gl",
			ruleID:       "api_keys.octopus-deploy-api-key.gl",
			input:        "const octopus_deploy_api_key = \"API-AAAAAAAAAAAAAAAAAAAAAAAAAA\";",
			wantFullText: "API-AAAAAAAAAAAAAAAAAAAAAAAAAA",
		},
		{
			name:         "api_keys.openai-api-key.gl",
			ruleID:       "api_keys.openai-api-key.gl",
			input:        "const openai_api_key = \"sk-proj-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaT3BlbkFJaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "sk-proj-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaT3BlbkFJaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.perplexity-api-key.gl",
			ruleID:       "api_keys.perplexity-api-key.gl",
			input:        "const perplexity_api_key = \"pplx-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "pplx-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.plaid-api-token.gl",
			ruleID:       "api_keys.plaid-api-token.gl",
			input:        "plaid_api_token = \"ACCESS-SANDBOX-aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa\";",
			wantFullText: "ACCESS-SANDBOX-aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		},
		{
			name:         "api_keys.planetscale-api-token.gl",
			ruleID:       "api_keys.planetscale-api-token.gl",
			input:        "const planetscale_api_token = \"pscale_tkn_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "pscale_tkn_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.postman-api-token.gl",
			ruleID:       "api_keys.postman-api-token.gl",
			input:        "const postman_api_token = \"PMAK-aaaaaaaaaaaaaaaaaaaaaaaa-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "PMAK-aaaaaaaaaaaaaaaaaaaaaaaa-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.prefect-api-token.gl",
			ruleID:       "api_keys.prefect-api-token.gl",
			input:        "const prefect_api_token = \"pnu_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "pnu_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.privateai-api-token.gl",
			ruleID:       "api_keys.privateai-api-token.gl",
			input:        "privateai_api_token = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.pulumi-api-token.gl",
			ruleID:       "api_keys.pulumi-api-token.gl",
			input:        "const pulumi_api_token = \"pul-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "pul-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.readme-api-token.gl",
			ruleID:       "api_keys.readme-api-token.gl",
			input:        "const readme_api_token = \"rdme_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "rdme_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.rubygems-api-token.gl",
			ruleID:       "api_keys.rubygems-api-token.gl",
			input:        "const rubygems_api_token = \"rubygems_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "rubygems_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.scalingo-api-token.gl",
			ruleID:       "api_keys.scalingo-api-token.gl",
			input:        "const scalingo_api_token = \"tk-us-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "tk-us-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.sendgrid-api-token.gl",
			ruleID:       "api_keys.sendgrid-api-token.gl",
			input:        "const sendgrid_api_token = \"SG.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "SG.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.sendinblue-api-token.gl",
			ruleID:       "api_keys.sendinblue-api-token.gl",
			input:        "const sendinblue_api_token = \"xkeysib-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-aaaaaaaaaaaaaaaa\";",
			wantFullText: "xkeysib-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-aaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.shippo-api-token.gl",
			ruleID:       "api_keys.shippo-api-token.gl",
			input:        "const shippo_api_token = \"shippo_live_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "shippo_live_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.snyk-api-token.gl",
			ruleID:       "api_keys.snyk-api-token.gl",
			input:        "snyk_api_token = \"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		},
		{
			name:         "api_keys.sonar-api-token.gl",
			ruleID:       "api_keys.sonar-api-token.gl",
			input:        "sonar_token = \"SQU_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "SQU_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.stripe-key",
			ruleID:       "api_keys.stripe-key",
			input:        "STRIPE_SECRET_KEY=\"sk_live_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "sk_live_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.stripe-restricted",
			ruleID:       "api_keys.stripe-restricted",
			input:        "STRIPE_RESTRICTED_KEY=\"rk_live_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"",
			wantFullText: "rk_live_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.telegram-bot-api-token.gl",
			ruleID:       "api_keys.telegram-bot-api-token.gl",
			input:        "telegram_bot_api_token = \"1111111111111111:Aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "1111111111111111:Aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.twilio-api-key.gl",
			ruleID:       "api_keys.twilio-api-key.gl",
			input:        "const twilio_api_key = \"SKaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "SKaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.twitch-api-token.gl",
			ruleID:       "api_keys.twitch-api-token.gl",
			input:        "twitch_api_token = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.twitter-api-key.gl",
			ruleID:       "api_keys.twitter-api-key.gl",
			input:        "twitter_api_key = \"aaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.twitter-bearer-token.gl",
			ruleID:       "api_keys.twitter-bearer-token.gl",
			input:        "twitter_bearer_token = \"AAAAAAAAAAAAAAAAAAAAAAaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "AAAAAAAAAAAAAAAAAAAAAAaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.typeform-api-token.gl",
			ruleID:       "api_keys.typeform-api-token.gl",
			input:        "typeform_api_token = \"TFP_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "TFP_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "api_keys.yandex-api-key.gl",
			ruleID:       "api_keys.yandex-api-key.gl",
			input:        "yandex_api_key = \"AQVNaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "AQVNaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.adobe-client-id.gl",
			ruleID:       "credentials.adobe-client-id.gl",
			input:        "adobe_client_id = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.adobe-client-secret.gl",
			ruleID:       "credentials.adobe-client-secret.gl",
			input:        "adobe_client_secret = \"p8e-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "p8e-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.amqp_connection",
			ruleID:       "credentials.amqp_connection",
			input:        "AMQP_URL=\"amqps://user:aaa@rabbitmq.internal/vhost\"",
			wantFullText: "amqps://user:aaa@rabbitmq.internal/vhost",
		},
		{
			name:         "credentials.api_key_header",
			ruleID:       "credentials.api_key_header",
			input:        "X-Api-Key: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.url_encoded_bearer_token",
			ruleID:       "credentials.url_encoded_bearer_token",
			input:        "https://app.example.test/callback?auth=Bearer%20sk_live_abc123XYZ987&state=ok",
			wantFullText: "sk_live_abc123XYZ987",
		},
		{
			name:         "credentials.nested_json_api_key",
			ruleID:       "credentials.nested_json_api_key",
			input:        "{\"request\":{\"headers\":{\"x-auth\":{\"api\":{\"key\":\"fm_prod_key_12ABcd34Ef56\"}}}}}",
			wantFullText: "fm_prod_key_12ABcd34Ef56",
		},
		{
			name:         "credentials.asana-client-id.gl",
			ruleID:       "credentials.asana-client-id.gl",
			input:        "asana_client_id = \"1111111111111111\";",
			wantFullText: "1111111111111111",
		},
		{
			name:         "credentials.asana-client-secret.gl",
			ruleID:       "credentials.asana-client-secret.gl",
			input:        "asana_client_secret = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.azure-ad-client-secret.gl",
			ruleID:       "credentials.azure-ad-client-secret.gl",
			input:        "azure_ad_client_secret = \"aaa1Q~aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaa1Q~aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.bitbucket-client-id.gl",
			ruleID:       "credentials.bitbucket-client-id.gl",
			input:        "bitbucket_client_id = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.bitbucket-client-secret.gl",
			ruleID:       "credentials.bitbucket-client-secret.gl",
			input:        "bitbucket_client_secret = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.db_connection",
			ruleID:       "credentials.db_connection",
			input:        "DATABASE_URL=\"POSTGRESQL://a:aaa@a\"",
			wantFullText: "POSTGRESQL://a:aaa@a",
		},
		{
			name:         "credentials.digitalocean-refresh-token.gl",
			ruleID:       "credentials.digitalocean-refresh-token.gl",
			input:        "digitalocean_refresh_token = \"DOR_V1_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "DOR_V1_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.discord-client-id.gl",
			ruleID:       "credentials.discord-client-id.gl",
			input:        "discord_client_id = \"111111111111111111\";",
			wantFullText: "111111111111111111",
		},
		{
			name:         "credentials.discord-client-secret.gl",
			ruleID:       "credentials.discord-client-secret.gl",
			input:        "discord_client_secret = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.facebook-secret.gl",
			ruleID:       "credentials.facebook-secret.gl",
			input:        "facebook_secret = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.finicity-client-secret.gl",
			ruleID:       "credentials.finicity-client-secret.gl",
			input:        "finicity_client_secret = \"aaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.github-oauth.gl",
			ruleID:       "credentials.github-oauth.gl",
			input:        "github_oauth = \"gho_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "gho_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.github-refresh-token.gl",
			ruleID:       "credentials.github-refresh-token.gl",
			input:        "github_refresh_token = \"ghr_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "ghr_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.gitlab-oauth-app-secret.gl",
			ruleID:       "credentials.gitlab-oauth-app-secret.gl",
			input:        "gitlab_oauth_app_secret = \"gloas-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "gloas-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.gitlab-session-cookie.gl",
			ruleID:       "credentials.gitlab-session-cookie.gl",
			input:        "Cookie: _gitlab_session=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa; Path=/; Secure",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.hashicorp-tf-password.gl",
			ruleID:       "credentials.hashicorp-tf-password.gl",
			input:        "administrator_login_password = \"aaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.http_auth_basic",
			ruleID:       "credentials.http_auth_basic",
			input:        "Authorization: Basic aaaaaaaa==",
			wantFullText: "aaaaaaaa==",
		},
		{
			name:         "credentials.http_auth_bearer",
			ruleID:       "credentials.http_auth_bearer",
			input:        "Authorization: Bearer aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.intra42-client-secret.gl",
			ruleID:       "credentials.intra42-client-secret.gl",
			input:        "intra42_client_secret = \"s-s4t2ud-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "s-s4t2ud-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.jwt-base64.gl",
			ruleID:       "credentials.jwt-base64.gl",
			input:        "jwt_base64 = \"ZXlKaGJHY2lPaUaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa==\"",
			wantFullText: "ZXlKaGJHY2lPaUaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa==",
		},
		{
			name:         "credentials.jwt.gl",
			ruleID:       "credentials.jwt.gl",
			input:        "Authorization: Bearer eyaaaaaaaaaaaaaaaaa.eyaaaaaaaaaaaaaaaaa.aaaaaaaaaa==",
			wantFullText: "eyaaaaaaaaaaaaaaaaa.eyaaaaaaaaaaaaaaaaa.aaaaaaaaaa==",
		},
		{
			name:         "credentials.linear-client-secret.gl",
			ruleID:       "credentials.linear-client-secret.gl",
			input:        "linear_client_secret = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.linkedin-client-id.gl",
			ruleID:       "credentials.linkedin-client-id.gl",
			input:        "linkedin_client_id = \"aaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.linkedin-client-secret.gl",
			ruleID:       "credentials.linkedin-client-secret.gl",
			input:        "linkedin_client_secret = \"aaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.looker-client-id.gl",
			ruleID:       "credentials.looker-client-id.gl",
			input:        "looker_client_id = \"aaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.looker-client-secret.gl",
			ruleID:       "credentials.looker-client-secret.gl",
			input:        "looker_client_secret = \"aaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.messagebird-client-id.gl",
			ruleID:       "credentials.messagebird-client-id.gl",
			input:        "messagebird_client_id = \"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		},
		{
			name:         "credentials.nats_connection",
			ruleID:       "credentials.nats_connection",
			input:        "NATS_URL=\"nats://client:aaa@nats.internal:4222\"",
			wantFullText: "nats://client:aaa@nats.internal:4222",
		},
		{
			name:         "credentials.new-relic-user-api-id.gl",
			ruleID:       "credentials.new-relic-user-api-id.gl",
			input:        "new_relic_user_api_id = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.nuget-config-password.gl",
			ruleID:       "credentials.nuget-config-password.gl",
			input:        "<configuration><packageSourceCredentials><github><add key=\"ClearTextPassword\" value=\"xxxxxxxx\" /></github></packageSourceCredentials></configuration>",
			wantFullText: "xxxxxxxx",
		},
		{
			name:         "credentials.oauth_body_secret",
			ruleID:       "credentials.oauth_body_secret",
			input:        "{\"client_secret\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"grant_type\":\"client_credentials\"}",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.oauth_query_secret",
			ruleID:       "credentials.oauth_query_secret",
			input:        "https://auth.example.test/callback?client_id=demo&access_token=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.password",
			ruleID:       "credentials.password",
			input:        "password: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.plaid-client-id.gl",
			ruleID:       "credentials.plaid-client-id.gl",
			input:        "plaid_client_id = \"aaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.planetscale-oauth-token.gl",
			ruleID:       "credentials.planetscale-oauth-token.gl",
			input:        "planetscale_oauth_token = \"pscale_oauth_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "pscale_oauth_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.planetscale-password.gl",
			ruleID:       "credentials.planetscale-password.gl",
			input:        "planetscale_password = \"PSCALE_PW_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "PSCALE_PW_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.redis_connection",
			ruleID:       "credentials.redis_connection",
			input:        "REDIS_URL=\"REDISS://:aaa@a\"",
			wantFullText: "REDISS://:aaa@a",
		},
		{
			name:         "credentials.sendbird-access-id.gl",
			ruleID:       "credentials.sendbird-access-id.gl",
			input:        "sendbird_access_id = \"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		},
		{
			name:         "credentials.shopify-shared-secret.gl",
			ruleID:       "credentials.shopify-shared-secret.gl",
			input:        "shopify_shared_secret = \"shpss_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "shpss_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.sidekiq-secret.gl",
			ruleID:       "credentials.sidekiq-secret.gl",
			input:        "BUNDLE_ENTERPRISE__CONTRIBSYS__COM=\"aaaaaaaa:aaaaaaaa\"",
			wantFullText: "aaaaaaaa:aaaaaaaa",
		},
		{
			name:         "credentials.sidekiq-sensitive-url.gl",
			ruleID:       "credentials.sidekiq-sensitive-url.gl",
			input:        "https://aaaaaaaa:aaaaaaaa@gems.contribsys.com/",
			wantFullText: "https://aaaaaaaa:aaaaaaaa@gems.contribsys.com",
		},
		{
			name:         "credentials.slack-config-refresh-token.gl",
			ruleID:       "credentials.slack-config-refresh-token.gl",
			input:        "slack_config_refresh_token = \"XOXE-1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "XOXE-1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.sumologic-access-id.gl",
			ruleID:       "credentials.sumologic-access-id.gl",
			input:        "sumologic_access_id = \"suaaaaaaaaaaaa\";",
			wantFullText: "suaaaaaaaaaaaa",
		},
		{
			name:         "credentials.twitter-access-secret.gl",
			ruleID:       "credentials.twitter-access-secret.gl",
			input:        "twitter_access_secret = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.twitter-api-secret.gl",
			ruleID:       "credentials.twitter-api-secret.gl",
			input:        "twitter_api_secret = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:         "credentials.url_with_creds",
			ruleID:       "credentials.url_with_creds",
			input:        "callback_url = \"HTTPS://a:aaa@a\";",
			wantFullText: "HTTPS://a:aaa@a",
		},
		{
			name:         "ip-addrs.ipv4",
			ruleID:       "ip-addrs.ipv4",
			input:        "client_ip=8.8.8.8",
			wantFullText: "8.8.8.8",
		},
		{
			name:         "ip-addrs.ipv4-cidr",
			ruleID:       "ip-addrs.ipv4-cidr",
			input:        "allowed_cidr=8.8.8.0/24",
			wantFullText: "8.8.8.0/24",
		},
		{
			name:         "ip-addrs.ipv4-private",
			ruleID:       "ip-addrs.ipv4-private",
			input:        "private_ip=10.1.2.3",
			wantFullText: "10.1.2.3",
		},
		{
			name:         "ip-addrs.ipv4-public",
			ruleID:       "ip-addrs.ipv4-public",
			input:        "public_ip=8.8.4.4",
			wantFullText: "8.8.4.4",
		},
		{
			name:         "ip-addrs.ipv6",
			ruleID:       "ip-addrs.ipv6",
			input:        "client_ipv6=[2001:4860:4860:0000:0000:0000:0000:8888]",
			wantFullText: "2001:4860:4860:0000:0000:0000:0000:8888",
		},
		{
			name:         "ip-addrs.ipv6-cidr",
			ruleID:       "ip-addrs.ipv6-cidr",
			input:        "allowed_ipv6_cidr=2001:4860:4860:0000:0000:0000:0000:8888/64",
			wantFullText: "2001:4860:4860:0000:0000:0000:0000:8888/64",
		},
		{
			name:         "ip-addrs.ipv6-private",
			ruleID:       "ip-addrs.ipv6-private",
			input:        "private_ipv6=[fd00:0000:0000:0000:0000:0000:0000:0001]",
			wantFullText: "fd00:0000:0000:0000:0000:0000:0000:0001",
		},
		{
			name:         "ip-addrs.ipv6-public",
			ruleID:       "ip-addrs.ipv6-public",
			input:        "public_ipv6=[2001:4860:4860:0000:0000:0000:0000:8844]",
			wantFullText: "2001:4860:4860:0000:0000:0000:0000:8844",
		},
		{
			name:         "pii.docs.inn-org",
			ruleID:       "pii.docs.inn-org",
			input:        "ИНН организации: 7707083893",
			wantFullText: "7707083893",
		},
		{
			name:         "pii.docs.inn-person",
			ruleID:       "pii.docs.inn-person",
			input:        "ИНН физлица: 500100732259",
			wantFullText: "500100732259",
		},
		{
			name:         "pii.docs.ogrn",
			ruleID:       "pii.docs.ogrn",
			input:        "ОГРН: 1027700132195",
			wantFullText: "1027700132195",
		},
		{
			name:         "pii.docs.ogrnip",
			ruleID:       "pii.docs.ogrnip",
			input:        "ОГРНИП: 304500116000157",
			wantFullText: "304500116000157",
		},
		{
			name:         "pii.docs.snils",
			ruleID:       "pii.docs.snils",
			input:        "СНИЛС: 112-233-445 95",
			wantFullText: "112-233-445 95",
		},
		{
			name:         "pii.docs.passport",
			ruleID:       "pii.docs.passport",
			input:        "паспорт 4509 123456",
			wantFullText: "4509 123456",
		},
		{
			name:         "pii.email",
			ruleID:       "pii.email",
			input:        "user email: person.name+test@example.com",
			wantFullText: "person.name+test@example.com",
		},
		{
			name:         "pii.fin.credit-card",
			ruleID:       "pii.fin.credit-card",
			input:        "card_number = \"4111 1111 1111 1111\"",
			wantFullText: "4111 1111 1111 1111",
		},
		{
			name:         "pii.fin.credit-card.context",
			ruleID:       "pii.fin.credit-card.context",
			input:        "оплата по карте 4279 3811 5566 7788 вчера",
			wantFullText: "4279 3811 5566 7788",
		},
		{
			name:         "pii.fin.iban",
			ruleID:       "pii.fin.iban",
			input:        "beneficiary_iban = \"GB82 WEST 1234 5698 7654 32\"",
			wantFullText: "GB82 WEST 1234 5698 7654 32",
		},
		{
			name:         "pii.phone-ru",
			ruleID:       "pii.phone-ru",
			input:        "contact_phone = \"+7-(111)-111-11-11\"",
			wantFullText: "+7-(111)-111-11-11",
		},
		{
			name:         "pii.fio-ru",
			ruleID:       "pii.fio-ru",
			input:        "Заявка на пропуск: Смирнова Анна Сергеевна, дата визита — 15 июля.",
			wantFullText: "Смирнова Анна Сергеевна",
		},
		{
			name:         "pii.fio-ru.initials",
			ruleID:       "pii.fio-ru.initials",
			input:        "Ответственный: Смирнова А.С., кабинет 14.",
			wantFullText: "Смирнова А.С.",
		},
		{
			name:         "pii.fio-ru.short",
			ruleID:       "pii.fio-ru.short",
			input:        "встреча с Анна Смирнова в офисе",
			wantFullText: "Анна Смирнова",
		},
		{
			name:         "pii.docs.kpp",
			ruleID:       "pii.docs.kpp",
			input:        "в реквизитах кпп 772543001 указано",
			wantFullText: "772543001",
		},
		{
			name:         "pii.docs.address",
			ruleID:       "pii.docs.address",
			input:        "привезите заказ на г. Москва, ул. Тверская 15 завтра",
			wantFullText: "г. Москва, ул. Тверская 15",
		},
		{
			name:         "pii.fin.cvc",
			ruleID:       "pii.fin.cvc",
			input:        "система не принимает cvc 456, хотя карта рабочая",
			wantFullText: "456",
		},
		{
			name:         "access_tokens.generic-token",
			ruleID:       "access_tokens.generic-token",
			input:        "мой токен доступа ABCxyz123456789qwertyuiop истёк",
			wantFullText: "ABCxyz123456789qwertyuiop",
		},
		{
			name:         "access_tokens.generic-long-token",
			ruleID:       "access_tokens.generic-long-token",
			input:        "старый ZXCasd123QWEzxc456RTYfgh789UIOjkl012 истёк",
			wantFullText: "ZXCasd123QWEzxc456RTYfgh789UIOjkl012",
		},
		{
			name:         "credentials.curl-auth-header.gl/basic-double",
			ruleID:       "credentials.curl-auth-header.gl",
			input:        "curl -H \"Authorization: Basic dGVzdGluZw==\" https://example.test ",
			wantFullText: "dGVzdGluZw==",
		},
		{
			name:         "credentials.curl-auth-header.gl/bearer-double",
			ruleID:       "credentials.curl-auth-header.gl",
			input:        "curl -H \"Authorization: Bearer abcdefgh\" https://example.test ",
			wantFullText: "abcdefgh",
		},
		{
			name:         "credentials.curl-auth-header.gl/raw-authorization-double",
			ruleID:       "credentials.curl-auth-header.gl",
			input:        "curl -H \"Authorization: abcdefgh\" https://example.test ",
			wantFullText: "abcdefgh",
		},
		{
			name:         "credentials.curl-auth-header.gl/x-api-key-double",
			ruleID:       "credentials.curl-auth-header.gl",
			input:        "curl -H \"X-Api-Key: abcdefgh\" https://example.test ",
			wantFullText: "abcdefgh",
		},
		{
			name:         "credentials.curl-auth-header.gl/basic-single",
			ruleID:       "credentials.curl-auth-header.gl",
			input:        "curl -H 'Authorization: Basic dGVzdGluZw==' https://example.test ",
			wantFullText: "dGVzdGluZw==",
		},
		{
			name:         "credentials.curl-auth-header.gl/bearer-single",
			ruleID:       "credentials.curl-auth-header.gl",
			input:        "curl -H 'Authorization: Bearer abcdefgh' https://example.test ",
			wantFullText: "abcdefgh",
		},
		{
			name:         "credentials.curl-auth-header.gl/raw-authorization-single",
			ruleID:       "credentials.curl-auth-header.gl",
			input:        "curl -H 'Authorization: abcdefgh' https://example.test ",
			wantFullText: "abcdefgh",
		},
		{
			name:         "credentials.curl-auth-header.gl/x-api-key-single",
			ruleID:       "credentials.curl-auth-header.gl",
			input:        "curl -H 'X-Api-Key: abcdefgh' https://example.test ",
			wantFullText: "abcdefgh",
		},
		{
			name:         "credentials.curl-auth-user.gl/double-quoted",
			ruleID:       "credentials.curl-auth-user.gl",
			input:        "curl -u \"user:password\" https://example.test ",
			wantFullText: "user:password",
		},
		{
			name:         "credentials.curl-auth-user.gl/single-quoted",
			ruleID:       "credentials.curl-auth-user.gl",
			input:        "curl -u 'user:password' https://example.test ",
			wantFullText: "user:password",
		},
		{
			name:         "credentials.curl-auth-user.gl/unquoted",
			ruleID:       "credentials.curl-auth-user.gl",
			input:        "curl -u user:password https://example.test ",
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
			input:        "jira_token = \"aaaaaaaaaaaaaaaaaaaabbbb\";",
			wantFullText: "aaaaaaaaaaaaaaaaaaaabbbb",
		},
		{
			name:         "api_keys.atlassian-api-token.gl/standalone",
			ruleID:       "api_keys.atlassian-api-token.gl",
			input:        "ATATT3AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA;",
			wantFullText: "ATATT3AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		},
	}
}

// --- Extra format scenarios (not part of the counted realConfigRuleCases) ---
//
// These exercise multiple real-world spellings per rule. They live here (rather
// than in realConfigRuleCases, whose length is pinned to expectedRealConfigCases)
// so adding scenarios does not require touching the count constants.

// validSNILS / invalidSNILS mirror the checksum fixtures in
// pkg/guardrails/regex/validation/validation_test.go.
const (
	validSNILS   = "11223344595"
	invalidSNILS = "11223344596"
)

// snilsFormat renders 11 raw digits with the given inter-group separator and
// tail separator, e.g. ("-", " ") -> "112-233-445 95".
func snilsFormat(raw, sep, tail string) string {
	return raw[0:3] + sep + raw[3:6] + sep + raw[6:9] + tail + raw[9:]
}

// TestSNILSFormats locks in the SNILS spellings that must be detected after the
// regex broadening: dashed canonical, space-grouped, partially-separated, and
// raw digits. All positive samples are valid by the SNILS checksum.
func TestSNILSFormats(t *testing.T) {
	t.Parallel()
	scanner, _ := loadRealConfigScanner(t)

	dashed := snilsFormat(validSNILS, "-", " ")         // 112-233-445 95
	spaced := snilsFormat(validSNILS, " ", " ")         // 112 233 445 95
	splitTail := validSNILS[0:9] + " " + validSNILS[9:] // 112233445 95
	raw := validSNILS                                   // 11223344595

	for _, tt := range []struct{ name, input, match string }{
		{"dashed-canonical", dashed, dashed},
		{"space-grouped", spaced, spaced},
		{"split-tail", splitTail, splitTail},
		{"raw-digits", raw, raw},
		{"in-sentence", "СНИЛС " + spaced + ", проверьте.", spaced},
		{"json", `{"snils":"` + dashed + `"}`, dashed},
	} {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertSingleMatch(t, scanner, "pii.docs.snils", tt.input, tt.match)
		})
	}

	// Invalid checksum must not match even in a supported format.
	assertNoMatch(t, scanner, "pii.docs.snils", snilsFormat(invalidSNILS, "-", " "))
}

// TestPassportFormats locks in passport detection: only the digit span is
// masked, context keywords are required, and bare 10-digit numbers without
// passport context are never matched (no collision with inn-org).
func TestPassportFormats(t *testing.T) {
	t.Parallel()
	scanner, _ := loadRealConfigScanner(t)

	const series, number = "4509", "123456"
	compact := series + " " + number         // 4509 123456
	seriesSplit := "45 09 " + number         // 45 09 123456
	withNomer := series + " номер " + number // 4509 номер 123456
	withSign := "45 09 № " + number          // 45 09 № 123456

	for _, tt := range []struct{ name, input, match string }{
		{"compact-spaced", "паспорт " + compact, compact},
		{"series-split", "паспорт " + seriesSplit, seriesSplit},
		{"with-nomer", "серия " + withNomer, withNomer},
		{"with-numbersign", "серия " + withSign, withSign},
		{"prefix-and-tail", "паспорт: серия " + compact + ", выдан", compact},
	} {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertSingleMatch(t, scanner, "pii.docs.passport", tt.input, tt.match)
		})
	}

	// No passport context -> no match (must not behave like a generic 10-digit rule).
	bare := series + number // 4509123456
	assertNoMatch(t, scanner, "pii.docs.passport", bare)
	assertNoMatch(t, scanner, "pii.docs.passport", "номер заказа "+bare+" готов")
}

// TestFioRuFormats locks in Russian full-name detection: the patronymic suffix
// (-вич/-вна/-ична) is the precision anchor, both word orders are covered
// (Фамилия Имя Отчество and Имя Отчество Фамилия, incl. hyphenated surnames),
// and capitalized sequences WITHOUT a patronymic never match.
func TestFioRuFormats(t *testing.T) {
	t.Parallel()
	scanner, _ := loadRealConfigScanner(t)

	for _, tt := range []struct{ name, input, match string }{
		{"surname-first", "пропуск для Смирнова Анна Сергеевна, стол 5", "Смирнова Анна Сергеевна"},
		{"surname-last", "командирован Иван Петрович Сидоров сегодня", "Иван Петрович Сидоров"},
		{"male-patronymic", "исполнитель: Кузнецов Пётр Иванович.", "Кузнецов Пётр Иванович"},
		{"ichna-patronymic", "заявитель Фомина Мария Ильинична принята", "Фомина Мария Ильинична"},
		{"hyphenated-surname", "докладчик Петрова-Водкина Анна Сергеевна выступит", "Петрова-Водкина Анна Сергеевна"},
		{"start-of-text", "Смирнова Анна Сергеевна оформила пропуск", "Смирнова Анна Сергеевна"},
		{"short-ich", "мастер Кузьмин Иван Ильич проверил", "Кузьмин Иван Ильич"},
		{"genitive", "заявление от Смирновой Анны Сергеевны получено", "Смирновой Анны Сергеевны"},
		{"dative-male", "выдать Иванову Ивану Ивановичу пропуск", "Иванову Ивану Ивановичу"},
		{"instrumental-male", "подписано Кузнецовым Петром Ивановичем вчера", "Кузнецовым Петром Ивановичем"},
		{"prepositional-female", "сведения о Павловой Ольге Сергеевне уточнены", "Павловой Ольге Сергеевне"},
		{"all-caps", "СМИРНОВА АННА СЕРГЕЕВНА — посетитель", "СМИРНОВА АННА СЕРГЕЕВНА"},
		{"all-caps-male", "пропуск: ИВАНОВ ИВАН ИВАНОВИЧ, стол 3", "ИВАНОВ ИВАН ИВАНОВИЧ"},
	} {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertSingleMatch(t, scanner, "pii.fio-ru", tt.input, tt.match)
		})
	}

	// No patronymic anchor -> no match FOR THIS RULE: bare pairs and initials
	// belong to pii.fio-ru.short / pii.fio-ru.initials; arbitrary capitalized
	// triples and ALL-CAPS headings must pass through entirely.
	assertNoMatch(t, scanner, "pii.fio-ru", "встреча с Анна Смирнова в офисе")
	assertNoMatch(t, scanner, "pii.fio-ru", "Общество Ромашка Москва открыло филиал")
	assertNoMatch(t, scanner, "pii.fio-ru", "Заявка На Пропуск оформлена")
	assertNoMatch(t, scanner, "pii.fio-ru", "ООО РОГА И КОПЫТА открыло филиал")
	assertNoMatch(t, scanner, "pii.fio-ru", "СРОЧНО ОФОРМИТЬ ПРОПУСК сегодня")
}

// TestFioInitialsFormats: surname + initials in both orders, lowercase and
// ALL-CAPS surnames, with and without a space between the initials. Single
// initials («пункт А.») and lone dotted abbreviations must not match.
func TestFioInitialsFormats(t *testing.T) {
	t.Parallel()
	scanner, _ := loadRealConfigScanner(t)

	for _, tt := range []struct{ name, input, match string }{
		{"surname-first", "подпись: Смирнова А.С. стоит", "Смирнова А.С."},
		{"surname-first-spaced", "исполнитель Кузнецов П. И. отвечает", "Кузнецов П. И."},
		{"initials-first", "докладчик А.С. Смирнова выступает", "А.С. Смирнова"},
		{"initials-first-spaced", "утвердил И. И. Иванов вчера", "И. И. Иванов"},
		{"caps-surname", "пропуск: СМИРНОВА А.С. выдан", "СМИРНОВА А.С."},
		{"hyphenated-surname", "автор Петрова-Водкина А.С. указана", "Петрова-Водкина А.С."},
		{"declined-surname", "заявление Смирновой А.С. принято", "Смирновой А.С."},
	} {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertSingleMatch(t, scanner, "pii.fio-ru.initials", tt.input, tt.match)
		})
	}

	assertNoMatch(t, scanner, "pii.fio-ru.initials", "см. пункт А. договора")
	assertNoMatch(t, scanner, "pii.fio-ru.initials", "формат А4 и лист Б5")
	assertNoMatch(t, scanner, "pii.fio-ru.initials", "т.е. вопрос закрыт")
}

// TestFioShortFormats: first-name dictionary anchors the two-word form.
// Branch A: dictionary name (with declension) + free-form capitalized
// surname; branch B: suffix-looking surname + dictionary name. Capitalized
// non-name pairs must pass through.
func TestFioShortFormats(t *testing.T) {
	t.Parallel()
	scanner, _ := loadRealConfigScanner(t)

	for _, tt := range []struct{ name, input, match string }{
		{"name-surname", "встреча с Анна Смирнова в офисе", "Анна Смирнова"},
		{"surname-name", "сотрудник Смирнова Анна на месте", "Смирнова Анна"},
		{"declined-pair", "пригласили Анну Смирнову на встречу", "Анну Смирнову"},
		{"male-pair", "заказ передан Иван Кузнецов лично", "Иван Кузнецов"},
		{"male-declined", "документы у Ивана Кузнецова остались", "Ивана Кузнецова"},
		{"yo-name", "дежурит Пётр Николаев сегодня", "Пётр Николаев"},
		{"non-suffix-surname", "менеджер Мария Жук ответила", "Мария Жук"},
		{"adjectival-surname-first", "сотрудник Белая Мария на месте", "Белая Мария"},
		{"adjectival-surname-decl", "заявитель Достоевская Анна пришла", "Достоевская Анна"},
		{"ko-surname", "звонил Иван Шевченко вчера", "Иван Шевченко"},
		{"name-patronymic", "Здравствуйте, Анна Сергеевна, проходите", "Анна Сергеевна"},
	} {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertSingleMatch(t, scanner, "pii.fio-ru.short", tt.input, tt.match)
		})
	}

	// Capitalized pairs without a dictionary-name anchor stay untouched —
	// including adjective-first phrases whose second word is not a name.
	assertNoMatch(t, scanner, "pii.fio-ru.short", "поезд Москва Ростов отправился")
	assertNoMatch(t, scanner, "pii.fio-ru.short", "Красная Площадь закрыта сегодня")
	assertNoMatch(t, scanner, "pii.fio-ru.short", "Совет Федерации собрался")
	assertNoMatch(t, scanner, "pii.fio-ru.short", "парад Красная Армия провела")
	assertNoMatch(t, scanner, "pii.fio-ru.short", "Ясная Поляна закрыта сегодня")
}

// TestKppCvcTokenFormats locks in the three keyword-anchored rules added to
// close the pii-bench 152-ФЗ coverage gaps: КПП, CVC and the generic token
// catch-all — each requires its context keyword, and bare numbers/strings
// without that anchor pass through.
// TestAddressFormats locks in the three address shapes and the precision
// guards: a street/apartment anchor is always required, a bare word is not.
func TestAddressFormats(t *testing.T) {
	t.Parallel()
	scanner, _ := loadRealConfigScanner(t)

	for _, tt := range []struct{ name, input, match string }{
		{"marker-first-city", "жду заказ на г. Москва, ул. Тверская 15 завтра", "г. Москва, ул. Тверская 15"},
		{"marker-first-apt", "адрес: ул. Гагарина 23 кв 45 приезжайте", "ул. Гагарина 23 кв 45"},
		{"marker-after-name", "жду на Невский проспект 88 сегодня", "Невский проспект 88"},
		{"marker-after-comma", "заказ на Ленинградский проспект, дом 45, квартира 12", "Ленинградский проспект, дом 45, квартира 12"},
		{"apartment-anchor", "жду доставку Чехова 44 кв 78 вечером", "Чехова 44 кв 78"},
		{"korpus-anchor", "адрес Московская 156 подъезд 2 тут", "Московская 156 подъезд 2"},
	} {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertSingleMatch(t, scanner, "pii.docs.address", tt.input, tt.match)
		})
	}
	// A street marker glued inside a word (артик-УЛ) must not fire; a bare
	// capitalized pair with no marker/suffix passes through.
	assertNoMatch(t, scanner, "pii.docs.address", "артикул товара 789012 закончился")
	assertNoMatch(t, scanner, "pii.docs.address", "поезд Москва Ростов прибыл")
}

// TestCreditCardContextFormats locks in the keyword-gated no-Luhn card fallback:
// a card-shaped number NEAR a card keyword is masked even if its Luhn checksum
// fails (typo / synthetic), while the keyword gate + brand-shape validator keep
// precision — no keyword, or a non-card shape, means no match.
func TestCreditCardContextFormats(t *testing.T) {
	t.Parallel()
	scanner, _ := loadRealConfigScanner(t)

	for _, tt := range []struct{ name, input, match string }{
		// Luhn-invalid but card-shaped, near a card keyword → masked.
		{"mir-by-karte", "оплата по карте 2200 3456 7890 1234 прошла", "2200 3456 7890 1234"},
		{"visa-by-card", "charged card 4279 3811 5566 7788 today", "4279 3811 5566 7788"},
	} {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertSingleMatch(t, scanner, "pii.fin.credit-card.context", tt.input, tt.match)
		})
	}
	// No card keyword → the fallback stays silent (Luhn rule already rejects it).
	assertNoMatch(t, scanner, "pii.fin.credit-card.context", "перевод 2200 3456 7890 1234 выполнен")
	// Keyword present but the number is not a valid card shape (13-digit OGRN-like)
	// → brand-shape validator rejects it.
	assertNoMatch(t, scanner, "pii.fin.credit-card.context", "карта 5123456789012 в реестре")
	// Precision guards from an adversarial FP hunt:
	// (1) omonym — a letter right after "карт..." (картЕЛЬ) means it is not a card word.
	assertNoMatch(t, scanner, "pii.fin.credit-card.context", "Картель по делу 2200 1122 3344 5566 обсуждали")
	// (2) a real card word but the card-shaped number is one WORD away (order/account
	//     number), not adjacent → not a PAN.
	assertNoMatch(t, scanner, "pii.fin.credit-card.context", "По карте заказ 4200123456789012 оформлен на доставку")
}

func TestKppCvcTokenFormats(t *testing.T) {
	t.Parallel()
	scanner, _ := loadRealConfigScanner(t)

	// КПП: keyword up to three filler words away; a bare 9-digit run is ignored.
	assertSingleMatch(t, scanner, "pii.docs.kpp", "кпп 772543001 в договоре", "772543001")
	assertSingleMatch(t, scanner, "pii.docs.kpp", "кпп в счете-фактуре 773345001, а", "773345001")
	assertNoMatch(t, scanner, "pii.docs.kpp", "номер заказа 772543001 готов")

	// CVC: cvc/cvv/цвс keyword; a generic 3-digit "код 789" is left alone.
	assertSingleMatch(t, scanner, "pii.fin.cvc", "цвс был 408, но система", "408")
	assertSingleMatch(t, scanner, "pii.fin.cvc", "нужен cvc 789 для оплаты", "789")
	assertSingleMatch(t, scanner, "pii.fin.cvc", "код на обороте карты 274 введите", "274")
	assertNoMatch(t, scanner, "pii.fin.cvc", "система не принимает код 789")
	assertNoMatch(t, scanner, "pii.fin.cvc", "три цифры пароля 123 неверны")

	// Token: keyword + 16+ alnum, OTP after a recovery-code keyword, and a
	// keyword-free 40+ char fallback.
	assertSingleMatch(t, scanner, "access_tokens.generic-token",
		"токен перестал работать: 7f8d9a2b4e1c6f3a8b5d2e9c4a7f1b8e3d6c9a2f5b8e1d4a7c. Мой",
		"7f8d9a2b4e1c6f3a8b5d2e9c4a7f1b8e3d6c9a2f5b8e1d4a7c")
	assertSingleMatch(t, scanner, "access_tokens.generic-token",
		"код восстановления 847593 отправлен", "847593")
	assertSingleMatch(t, scanner, "access_tokens.generic-token",
		"используйте API ключ api_3f8e9d2c1b4a5f6e7d8c9b0a1f2e3d4 для доступа",
		"api_3f8e9d2c1b4a5f6e7d8c9b0a1f2e3d4")
	// The long-token fallback catches mixed-case tokens but NOT lowercase hex
	// hashes / UUIDs (they read as ordinary technical identifiers).
	assertSingleMatch(t, scanner, "access_tokens.generic-long-token",
		"старый pQ6rS8tU0vX3yZ5aB7cD9eF1gH3jK5mN7pR9sT истёк",
		"pQ6rS8tU0vX3yZ5aB7cD9eF1gH3jK5mN7pR9sT")
	assertNoMatch(t, scanner, "access_tokens.generic-long-token",
		"commit a1b2c3d4e5f6789012345678901234567890abcd смержен")
	assertNoMatch(t, scanner, "access_tokens.generic-long-token",
		"UUID 550e8400e29b41d4a716446655440000 в базе")
}

// TestPassportContextInflections checks the widened passport rule: the keyword
// may be inflected and sit a few words before the number.
func TestPassportContextInflections(t *testing.T) {
	t.Parallel()
	scanner, _ := loadRealConfigScanner(t)

	for _, tt := range []struct{ name, input, match string }{
		{"inflected-instrumental", "так с моим паспортом? 4609 328145 система", "4609 328145"},
		{"inflected-adjective", "паспортные данные: 4518123456 подтверждаю", "4518123456"},
		{"two-two-six", "блокировка по паспорту 77 12 345678. Очень", "77 12 345678"},
		{"ten-digit", "могу подтвердить паспорт 5012345678, хочу", "5012345678"},
	} {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertSingleMatch(t, scanner, "pii.docs.passport", tt.input, tt.match)
		})
	}
	// Still needs the keyword: a bare 10-digit number is not a passport.
	assertNoMatch(t, scanner, "pii.docs.passport", "заказ номер 5012345678 оформлен")
}

// TestPhoneFormats locks in the RU phone boundary behaviour: real spellings are
// detected (only the number is masked, not any leading guard char), while a
// phone glued to a longer digit run or trailed by an extra digit is rejected.
// Phone values are built from split literals so the source has no contiguous
// number.
func TestPhoneFormats(t *testing.T) {
	t.Parallel()
	scanner, _ := loadRealConfigScanner(t)

	p8 := "8" + "999" + "123" + "45" + "67"     // 89991234567
	pPlus := "+7" + "999" + "123" + "45" + "67" // +79991234567
	pParen := "8 (" + "999" + ") " + "123" + "-45" + "-67"

	for _, tt := range []struct{ name, input, match string }{
		{"plus-at-start", pPlus, pPlus},
		{"eight-paren-dash", pParen, pParen},
		{"preceded-by-word", "тел. " + pPlus, pPlus},
		{"json-quoted", `{"phone":"` + p8 + `"}`, p8},
		{"trailing-comma", p8 + ", позвоните", p8},
	} {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertSingleMatch(t, scanner, "pii.phone-ru", tt.input, tt.match)
		})
	}

	// Substring of a longer digit run (e.g. a card) must not be taken as a phone.
	assertNoMatch(t, scanner, "pii.phone-ru", "512"+p8)
	// Trailing extra digit means it is not a valid 11-digit RU number.
	assertNoMatch(t, scanner, "pii.phone-ru", p8+"8")
}

func assertSingleMatch(t *testing.T, scanner *sensitive.Scanner, ruleID, input, want string) {
	t.Helper()
	m, err := scanner.Scan(input, []string{ruleID})
	if err != nil {
		t.Fatalf("Scan error: %v", err)
	}
	if len(m) != 1 || m[0].FullText != want {
		t.Fatalf("input=%q: got %+v, want single match %q", input, m, want)
	}
}

func assertNoMatch(t *testing.T, scanner *sensitive.Scanner, ruleID, input string) {
	t.Helper()
	m, err := scanner.Scan(input, []string{ruleID})
	if err != nil {
		t.Fatalf("Scan error: %v", err)
	}
	if len(m) != 0 {
		t.Fatalf("input=%q: expected no match, got %+v", input, m)
	}
}
