package delete

// UseCase removes custom guardrail rules.
type UseCase struct {
	store    RuleStore
	builtins Builtins
	reloader Reloader
}

// NewUseCase creates a new UseCase.
func NewUseCase(store RuleStore, builtins Builtins, reloader Reloader) *UseCase {
	return &UseCase{store: store, builtins: builtins, reloader: reloader}
}
