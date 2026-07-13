// Package setenabled implements the "enable or disable a rule" use case. It is
// the only mutation permitted on built-in rules. It toggles the disabled flag,
// reloads the registry snapshot, and returns the resulting rule view.
package setenabled

import (
	"context"

	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
)

// Command is the input for the setenabled use case.
type Command struct {
	ID      string
	Enabled bool
}

// CommandResponse is the output of the setenabled use case: the affected rule,
// whether it is built-in, and its resulting enabled state.
type CommandResponse struct {
	Rule    rule.Rule
	Builtin bool
	Enabled bool
}

//go:generate mockgen -source=contract.go -destination=mock_test.go -package=setenabled_test

// CommandHandler handles the setenabled use case.
type CommandHandler interface {
	Handle(ctx context.Context, cmd Command) (CommandResponse, error)
}

// RuleStore reads custom rules and toggles the disabled flag.
type RuleStore interface {
	GetRule(ctx context.Context, id string) (rule.Rule, error)
	SetRuleDisabled(ctx context.Context, id string, disabled bool) error
}

// Builtins reports and returns immutable built-in rules.
type Builtins interface {
	IsBuiltin(id string) bool
	Get(id string) (rule.Rule, bool)
}

// Reloader rebuilds and atomically swaps the compiled registry snapshot.
type Reloader interface {
	Reload(ctx context.Context) error
}
