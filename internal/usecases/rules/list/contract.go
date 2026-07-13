// Package list implements the "list rules" use case, returning built-in and/or
// custom rules (sorted, custom by ID) each annotated with its source and
// enabled state. The controller decides which sources to include.
package list

import (
	"context"

	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
)

// Command is the input for the list use case. The two flags select which rule
// sources to include; the controller maps the ?source= query onto them.
type Command struct {
	IncludeBuiltin bool
	IncludeCustom  bool
}

// RuleView is one listed rule annotated with its source and enabled state.
type RuleView struct {
	Rule    rule.Rule
	Builtin bool
	Enabled bool
}

// CommandResponse is the output of the list use case.
type CommandResponse struct {
	Rules []RuleView
}

//go:generate mockgen -source=contract.go -destination=mock_test.go -package=list_test

// CommandHandler handles the list use case.
type CommandHandler interface {
	Handle(ctx context.Context, cmd Command) (CommandResponse, error)
}

// RuleStore lists custom rules and the disabled set.
type RuleStore interface {
	ListRules(ctx context.Context) ([]rule.Rule, error)
	ListDisabledRuleIDs(ctx context.Context) ([]string, error)
}

// Builtins lists immutable built-in rules.
type Builtins interface {
	List() []rule.Rule
}
