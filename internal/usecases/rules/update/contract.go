// Package update implements the "replace an existing custom rule" use case:
// reject built-in IDs, validate the new content, require the custom rule to
// exist, persist it, then reload the registry snapshot.
package update

import (
	"context"

	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
)

// Command is the input for the update use case.
type Command struct {
	Rule rule.Rule
}

// CommandResponse is the output of the update use case. Enabled reflects the
// rule's current disabled-set state (updating content does not change it).
type CommandResponse struct {
	Rule    rule.Rule
	Enabled bool
}

//go:generate mockgen -source=contract.go -destination=mock_test.go -package=update_test

// CommandHandler handles the update use case.
type CommandHandler interface {
	Handle(ctx context.Context, cmd Command) (CommandResponse, error)
}

// RuleStore persists custom rules and reports their state.
type RuleStore interface {
	GetRule(ctx context.Context, id string) (rule.Rule, error)
	SaveRule(ctx context.Context, r rule.Rule) error
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
