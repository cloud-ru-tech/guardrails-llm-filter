// Package get implements the "read one rule" use case, resolving whether the
// ID is built-in or custom and its current enabled state.
package get

import (
	"context"

	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
)

// Command is the input for the get use case.
type Command struct {
	ID string
}

// CommandResponse is the output of the get use case: the rule, whether it is
// built-in, and its enabled state.
type CommandResponse struct {
	Rule    rule.Rule
	Builtin bool
	Enabled bool
}

//go:generate mockgen -source=contract.go -destination=mock_test.go -package=get_test

// CommandHandler handles the get use case.
type CommandHandler interface {
	Handle(ctx context.Context, cmd Command) (CommandResponse, error)
}

// RuleStore reads custom rules and the disabled set.
type RuleStore interface {
	GetRule(ctx context.Context, id string) (rule.Rule, error)
	ListDisabledRuleIDs(ctx context.Context) ([]string, error)
}

// Builtins returns immutable built-in rules by ID.
type Builtins interface {
	Get(id string) (rule.Rule, bool)
}
