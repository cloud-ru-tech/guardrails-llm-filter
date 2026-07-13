package rule

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAll_GroupPriorityAndDataTypeInheritance(t *testing.T) {
	t.Parallel()

	const yamlText = `
guardrails_regex_rules:
  - data_type: 42
    group_priority: 900
    name: test.group
    display_name: "Test"
    description: "Desc"
    rules:
      - rule_id: "test.rule"
        name: test.rule
        regex: '\bfoo\b'
        masking:
          placeholder: "TEST"
`

	dir := t.TempDir()
	path := filepath.Join(dir, "rules.yaml")
	if err := os.WriteFile(path, []byte(yamlText), 0o600); err != nil {
		t.Fatalf("write temp yaml: %v", err)
	}

	dataTypes, rules, err := LoadAll(path)
	if err != nil {
		t.Fatalf("LoadAll error: %v", err)
	}
	if len(dataTypes) != 1 {
		t.Fatalf("len(dataTypes)=%d, want 1", len(dataTypes))
	}
	if dataTypes[0].DataType != 42 || dataTypes[0].GroupPriority != 900 {
		t.Fatalf("dataTypes[0]=%+v", dataTypes[0])
	}
	if len(rules) != 1 {
		t.Fatalf("len(rules)=%d, want 1", len(rules))
	}
	if rules[0].DataType != 42 || rules[0].Group != "test.group" {
		t.Fatalf("rule inheritance failed: %+v", rules[0])
	}
}

const testRulesFileTemplate = `
guardrails_regex_rules:
  - data_type: 1
    group_priority: 100
    name: CREDENTIALS
    display_name: "Учетные данные"
    description: "test"
    rules:
      - rule_id: "%s"
        name: "%s"
        regex: "test_[a-z0-9]+"
        masking:
          placeholder: "TEST"
`

func writeRulesFile(t *testing.T, dir, fileName, ruleID string) string {
	t.Helper()

	path := filepath.Join(dir, fileName)
	content := []byte(strings.TrimSpace(fmt.Sprintf(testRulesFileTemplate, ruleID, ruleID)) + "\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write rules file %s: %v", path, err)
	}
	return path
}

func TestLoadAllFromFiles_LoadsBothFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	f1 := writeRulesFile(t, dir, "rules1.yaml", "r1")
	f2 := writeRulesFile(t, dir, "rules2.yaml", "r2")

	dataTypes, rules, err := LoadAllFromFiles(f1, f2)
	if err != nil {
		t.Fatalf("LoadAllFromFiles error: %v", err)
	}
	if len(dataTypes) != 2 {
		t.Fatalf("len(dataTypes)=%d, want 2", len(dataTypes))
	}
	if len(rules) != 2 {
		t.Fatalf("len(rules)=%d, want 2", len(rules))
	}
}

func TestLoadAllFromFiles_DuplicateRuleIDAcrossFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	f1 := writeRulesFile(t, dir, "rules1.yaml", "dup")
	f2 := writeRulesFile(t, dir, "rules2.yaml", "dup")

	_, _, err := LoadAllFromFiles(f1, f2)
	if err == nil {
		t.Fatal("expected duplicate rule_id error, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate guardrails rule_id") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadAllFromFiles_EmptyInput(t *testing.T) {
	t.Parallel()

	_, _, err := LoadAllFromFiles("", "   ")
	if err == nil {
		t.Fatal("expected error for empty input, got nil")
	}
}
