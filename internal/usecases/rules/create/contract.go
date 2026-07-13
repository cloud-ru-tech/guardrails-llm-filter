// Package create implements the "create a custom rule" use case: validate the
// rule, reject IDs that collide with a built-in or an existing custom rule,
// persist it, then reload the registry snapshot.
package create

import (
	"context"

	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
)

// Command is the input for the create use case.
type Command struct {
	Rule rule.Rule
}

// CommandResponse is the output of the create use case. Enabled reflects the
// rule's actual disabled-set state after creation (a freshly created rule is
// cleared to enabled), not a hardcoded assumption.
type CommandResponse struct {
	Rule    rule.Rule
	Enabled bool
}

//go:generate mockgen -source=contract.go -destination=mock_test.go -package=create_test

// CommandHandler handles the create use case.
type CommandHandler interface {
	Handle(ctx context.Context, cmd Command) (CommandResponse, error)
}

// RuleStore persists custom rules with atomic create-if-absent semantics and
// lists them so the count limit can be enforced before a write.
type RuleStore interface {
	CreateRule(ctx context.Context, r rule.Rule) error
	ListRules(ctx context.Context) ([]rule.Rule, error)
	// SetRuleDisabled clears (false) or sets (true) a rule's disabled flag.
	// Creation clears it so re-creating an ID that carries a lingering flag
	// from a prior delete/setenabled cannot resurrect it disabled.
	SetRuleDisabled(ctx context.Context, id string, disabled bool) error
	// ListDisabledRuleIDs reports the disabled set so the response can state the
	// rule's true enabled state instead of assuming it.
	ListDisabledRuleIDs(ctx context.Context) ([]string, error)
}

// Builtins reports whether an ID belongs to an immutable built-in rule.
type Builtins interface {
	IsBuiltin(id string) bool
}

// Reloader rebuilds and atomically swaps the compiled registry snapshot.
type Reloader interface {
	Reload(ctx context.Context) error
}
