package get

import (
	"context"
	"errors"
	"fmt"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
	ruleerrors "github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/errors"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
)

// Handle resolves one rule by ID (built-in or custom) with its enabled state.
func (uc *UseCase) Handle(ctx context.Context, cmd Command) (CommandResponse, error) {
	id := cmd.ID

	disabled, err := uc.store.ListDisabledRuleIDs(ctx)
	if err != nil {
		return CommandResponse{}, fmt.Errorf("list disabled rule ids: %w", err)
	}

	var (
		r       rule.Rule
		builtin bool
	)
	if got, ok := uc.builtins.Get(id); ok {
		r, builtin = got, true
	} else {
		got, err := uc.store.GetRule(ctx, id)
		if errors.Is(err, repository.ErrNotFound) {
			return CommandResponse{}, fmt.Errorf("%w: %q", ruleerrors.ErrNotFound, id)
		} else if err != nil {
			return CommandResponse{}, fmt.Errorf("get rule: %w", err)
		}
		r = got
	}

	enabled := true
	for _, d := range disabled {
		if d == id {
			enabled = false
			break
		}
	}
	return CommandResponse{Rule: r, Builtin: builtin, Enabled: enabled}, nil
}
