package rules_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/registry"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/scanners/sensitive"
	"github.com/cloud-ru-tech/guardrails-llm-filter/tests/testutil"
)

func loadRealConfigScanner(t *testing.T) (*sensitive.Scanner, map[string]rule.Rule) {
	t.Helper()

	root := testutil.RepoRoot(t)
	_, rules, err := rule.LoadAllFromFiles(
		filepath.Join(root, "configs/guardrails_regex_rules.gitleaks.generated.yaml"),
		filepath.Join(root, "configs/guardrails_regex_rules.yaml"),
	)
	if err != nil {
		t.Fatalf("LoadAllFromFiles error: %v", err)
	}

	rulesByID := make(map[string]rule.Rule, len(rules))
	for _, rl := range rules {
		rulesByID[rl.ID] = rl
	}

	reg := registry.NewRegistry()
	reg.Register(rules...)
	return sensitive.New(reg), rulesByID
}

func runeInClass(r rune, ranges []rune) bool {
	for i := 0; i+1 < len(ranges); i += 2 {
		if r >= ranges[i] && r <= ranges[i+1] {
			return true
		}
	}
	return false
}

func assertNoCapturedBoundaryLeak(t *testing.T, rl rule.Rule, fullText string) {
	t.Helper()
	if len(rl.Masking.CaptureGroups) == 0 {
		return
	}
	if fullText == "" {
		t.Fatal("captured FullText is empty")
	}
	if strings.HasPrefix(fullText, `\"`) || strings.HasSuffix(fullText, `\"`) {
		t.Fatalf("captured FullText leaks escaped JSON quote: %q", fullText)
	}
	if strings.ContainsAny(fullText[:1], " \t\r\n`'\";=,") {
		t.Fatalf("captured FullText leaks left boundary: %q", fullText)
	}
	if strings.ContainsAny(fullText[len(fullText)-1:], " \t\r\n`'\";,") {
		t.Fatalf("captured FullText leaks right boundary: %q", fullText)
	}
}

