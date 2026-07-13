package rules_test

import (
	"path/filepath"
	"testing"

	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/registry"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/scanners/sensitive"
	"github.com/cloud-ru-tech/guardrails-llm-filter/tests/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRealConfigPrefilterIsRecallPreservingOnUnicode is an integration guard for
// the keyword pre-filter fold fix: over the full shipped ruleset, scanning with
// the pre-filter ON must return exactly the same matches as with it OFF, even
// for bodies containing non-ASCII / case-folded text. Any rule whose keyword
// could diverge under strings.ToLower must have been marked ineligible at
// compile time, so recall is preserved.
func TestRealConfigPrefilterIsRecallPreservingOnUnicode(t *testing.T) {
	t.Parallel()

	root := testutil.RepoRoot(t)
	_, rules, err := rule.LoadAllFromFiles(
		filepath.Join(root, "configs/guardrails_regex_rules.gitleaks.generated.yaml"),
		filepath.Join(root, "configs/guardrails_regex_rules.yaml"),
	)
	require.NoError(t, err)

	reg := registry.NewRegistry()
	reg.Register(rules...)

	off := sensitive.New(reg)
	on := sensitive.New(reg, sensitive.WithKeywordPrefilter(true))

	ruleIDs := make([]string, 0, len(rules))
	for _, rl := range rules {
		ruleIDs = append(ruleIDs, rl.ID)
	}

	bodies := []string{
		"Bearer sk-ABCDEFGHIJKLMNOPQRSTUVWX secret=пароль",
		"ключ ΣΤΑ ςτα значение api_key=deadbeefdeadbeefdeadbeefdeadbeef",
		"straße SECRET ſecret Σ σ ς mixed-case AWS_SECRET",
		"нормальный текст без секретов",
	}

	for _, body := range bodies {
		wantOff, err := off.Scan(body, ruleIDs)
		require.NoError(t, err)
		gotOn, err := on.Scan(body, ruleIDs)
		require.NoError(t, err)

		assert.ElementsMatch(t, wantOff, gotOn,
			"pre-filter changed matches for body %q — recall not preserved", body)
	}
}
