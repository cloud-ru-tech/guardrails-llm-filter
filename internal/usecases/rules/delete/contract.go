// Package delete implements the "remove a custom rule" use case: reject
// built-in IDs, delete the custom rule, clear any lingering disabled flag,
// then reload the registry snapshot.
package delete

import "context"

// Command is the input for the delete use case.
type Command struct {
	ID string
}

// CommandResponse is the (empty) output of the delete use case.
type CommandResponse struct{}

//go:generate mockgen -source=contract.go -destination=mock_test.go -package=delete_test

// CommandHandler handles the delete use case.
type CommandHandler interface {
	Handle(ctx context.Context, cmd Command) (CommandResponse, error)
}

// RuleStore removes custom rules and clears their disabled flag.
type RuleStore interface {
	DeleteRule(ctx context.Context, id string) error
	SetRuleDisabled(ctx context.Context, id string, disabled bool) error
}

// Builtins reports whether an ID belongs to an immutable built-in rule.
type Builtins interface {
	IsBuiltin(id string) bool
}

// Reloader rebuilds and atomically swaps the compiled registry snapshot.
type Reloader interface {
	Reload(ctx context.Context) error
}
