package placeholder

import "github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/registry"

//go:generate mockgen -source=scanner.go -destination=mock_test.go -package=placeholder_test

// Registry resolves compiled rules for the given rule IDs.
type Registry interface {
	GetCompiledRulesByRuleIDs(ruleIDs []string) []registry.CompiledRule
}

// Scanner finds placeholder matches for the given rule IDs.
type Scanner struct {
	registry Registry
}

// New creates a new Scanner with explicit runtime dependencies.
func New(registry Registry) *Scanner {
	return &Scanner{registry: registry}
}
