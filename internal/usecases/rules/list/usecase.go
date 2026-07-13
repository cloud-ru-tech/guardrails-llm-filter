package list

// UseCase lists guardrail rules with their source and enabled state.
type UseCase struct {
	store    RuleStore
	builtins Builtins
}

// NewUseCase creates a new UseCase.
func NewUseCase(store RuleStore, builtins Builtins) *UseCase {
	return &UseCase{store: store, builtins: builtins}
}
