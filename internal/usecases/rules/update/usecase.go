package update

import "github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/validation"

// UseCase replaces existing custom guardrail rules.
type UseCase struct {
	store    RuleStore
	builtins Builtins
	reloader Reloader
	limits   validation.Limits
}

// NewUseCase creates a new UseCase.
func NewUseCase(store RuleStore, builtins Builtins, reloader Reloader, limits validation.Limits) *UseCase {
	return &UseCase{store: store, builtins: builtins, reloader: reloader, limits: limits}
}
