package get

// UseCase reads a single guardrail rule.
type UseCase struct {
	store    RuleStore
	builtins Builtins
}

// NewUseCase creates a new UseCase.
func NewUseCase(store RuleStore, builtins Builtins) *UseCase {
	return &UseCase{store: store, builtins: builtins}
}
