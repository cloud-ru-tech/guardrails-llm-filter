// Package errors holds the domain sentinel errors shared by every rule
// use case. Centralizing them keeps the scenario packages free of circular
// imports and gives the controller a single, stable place to map rule
// failures onto HTTP statuses. Import aliased (e.g. ruleerrors) to avoid
// shadowing the standard library.
package errors

import "errors"

// Sentinel errors mapped to HTTP statuses by the controller.
var (
	// ErrNotFound is returned when a rule (or the custom rule targeted by a
	// mutation) does not exist.
	ErrNotFound = errors.New("rule not found")
	// ErrBuiltin is returned when a mutation targets a built-in rule that is
	// immutable via the API (disabling is the only permitted exception).
	ErrBuiltin = errors.New("rule is built-in and immutable via the API")
	// ErrAlreadyExists is returned when creating a rule whose ID is already
	// taken by a built-in or custom rule.
	ErrAlreadyExists = errors.New("rule already exists")
	// ErrTooManyRules is returned when creating a rule would exceed the
	// configured maximum number of custom rules.
	ErrTooManyRules = errors.New("too many custom rules")
)

// ValidationError wraps a rule-content problem (bad regex, unknown validator,
// bad data type, bad ID); the controller maps it to 400.
type ValidationError struct{ Err error }

func (e *ValidationError) Error() string { return e.Err.Error() }
func (e *ValidationError) Unwrap() error { return e.Err }
