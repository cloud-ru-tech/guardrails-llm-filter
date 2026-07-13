package sensitive

import "github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/registry"

// Registry resolves rule IDs and masking policies for the masking flow.
//
//go:generate mockgen -source=scanner.go -destination=mock_test.go -package=sensitive_test
type Registry interface {
	GetCompiledRulesByRuleIDs(ruleIDs []string) []registry.CompiledRule
}

// Scanner finds sensitive values in text.
type Scanner struct {
	registry Registry

	// keywordPrefilter, when true, skips a rule's regex unless at least one
	// of the rule's declared keywords is present in the text. Rules without
	// keywords are always scanned.
	keywordPrefilter bool
}

// Option configures an optional Scanner behavior.
type Option func(*Scanner)

// WithKeywordPrefilter enables (or disables) the keyword pre-filter. Off by
// default: enabling it trades detection recall for scan speed.
func WithKeywordPrefilter(on bool) Option {
	return func(s *Scanner) { s.keywordPrefilter = on }
}

// New creates a new Scanner with explicit runtime dependencies.
func New(registry Registry, opts ...Option) *Scanner {
	s := &Scanner{registry: registry}
	for _, opt := range opts {
		opt(s)
	}
	return s
}
