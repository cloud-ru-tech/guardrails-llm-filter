// Package keywordprefilter benchmarks the sensitive scanner with and without
// the keyword pre-filter (GUARDRAILS_KEYWORD_PREFILTER_ENABLED) on the real
// shipped ruleset, to decide whether the pre-filter earns its keeping for the
// LLM-request-body workload.
package keywordprefilter

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/registry"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/scanners/sensitive"
	"github.com/cloud-ru-tech/guardrails-llm-filter/tests/testutil"
)

// defaultDataTypes covers the five built-in data types. (The shipped default
// GUARDRAILS_DATA_TYPES also enables 6/CUSTOM, but no built-in rule uses it, so
// it is inert for this built-in-rules benchmark.)
var defaultDataTypes = []uint32{1, 2, 3, 4, 5}

func loadRegistry(tb testing.TB) *registry.Registry {
	tb.Helper()
	root := testutil.RepoRoot(tb)
	_, rules, err := rule.LoadAllFromFiles(
		filepath.Join(root, "configs/guardrails_regex_rules.gitleaks.generated.yaml"),
		filepath.Join(root, "configs/guardrails_regex_rules.yaml"),
	)
	if err != nil {
		tb.Fatalf("LoadAllFromFiles: %v", err)
	}
	reg := registry.NewRegistry()
	reg.Register(rules...)
	return reg
}

// fixtures span the realistic spectrum of request bodies. The pre-filter's
// value depends heavily on the input: clean text lets it skip nearly every
// regex, while keyword-dense text defeats it.
var fixtures = map[string]string{
	// Typical clean prompt: no secrets, no keyword triggers. The common case,
	// and the pre-filter's best case (almost every rule is skipped).
	"clean_short": `Can you explain how a B-tree index speeds up range queries in a
relational database, and when a hash index would be a better choice instead?`,

	// Large clean prompt (~4 KB of prose/code with no secrets or trigger words).
	// Shows how the whole-body ToLower + hundreds of substring scans scale.
	"clean_large": strings.Repeat(`The quick brown fox jumps over the lazy dog while
reviewing the quarterly report about migratory bird patterns in the northern hemisphere. `, 40),

	// Realistic prompt carrying actual secrets/PII with their usual context words
	// (vendor names, "token"), so the pre-filter can skip fewer rules.
	"with_secrets": `Please help me debug this deploy. My config has
VAULT_TOKEN=hvs.CAESIJ0aBbCcDdEeFfGgHhIiJjKkLlMmNnOoPpQqRrSsTtUuVvWwXxYyZz01234567890aAbBcCdDeEfFgGhHiIjJkK
and the AWS key aws_access_key_id=AKIAIOSFODNN7EXAMPLE, contact me at john.doe@example.com,
card 4111111111111111. The database dsn is postgres://user:pass@10.0.0.5:5432/app.`,

	// Prose that happens to contain common gitleaks keyword words ("token",
	// "key", "password", "secret") but no real secrets — the pre-filter's worst
	// case: it cannot skip those rules yet finds nothing.
	"keyword_dense": `I keep forgetting the difference between a token and a key in
authentication. When the password is wrong the secret rotation fails and the api key
must be reissued. How does the auth token relate to the session secret and access key?`,
}

func BenchmarkScan(b *testing.B) {
	reg := loadRegistry(b)
	ruleIDs := reg.GetRuleIDsByDataTypes(defaultDataTypes...)
	if len(ruleIDs) == 0 {
		b.Fatal("no rule IDs resolved for default data types")
	}
	b.Logf("scanning against %d rules", len(ruleIDs))

	scanners := map[string]*sensitive.Scanner{
		"off": sensitive.New(reg),
		"on":  sensitive.New(reg, sensitive.WithKeywordPrefilter(true)),
	}

	// Stable iteration order so benchstat rows line up run-to-run.
	fixtureNames := []string{"clean_short", "clean_large", "with_secrets", "keyword_dense"}
	prefilterModes := []string{"off", "on"}

	for _, fx := range fixtureNames {
		text := fixtures[fx]
		for _, mode := range prefilterModes {
			sc := scanners[mode]
			b.Run(fmt.Sprintf("fixture=%s/prefilter=%s", fx, mode), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(text)))
				for b.Loop() {
					if _, err := sc.Scan(text, ruleIDs); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
