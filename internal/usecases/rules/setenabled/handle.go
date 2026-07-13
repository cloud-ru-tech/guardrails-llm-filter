package setenabled

import (
	"context"
	"errors"
	"fmt"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
	ruleerrors "github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/errors"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
)

// Handle enables or disables one rule (built-in or custom) and reloads the
// registry. Disabling is the only mutation permitted on built-in rules.
func (uc *UseCase) Handle(ctx context.Context, cmd Command) (CommandResponse, error) {
	id := cmd.ID
	builtin := uc.builtins.IsBuiltin(id)

	if !builtin {
		if _, err := uc.store.GetRule(ctx, id); errors.Is(err, repository.ErrNotFound) {
			return CommandResponse{}, fmt.Errorf("%w: %q", ruleerrors.ErrNotFound, id)
		} else if err != nil {
			return CommandResponse{}, fmt.Errorf("check rule existence: %w", err)
		}
	}
	if err := uc.store.SetRuleDisabled(ctx, id, !cmd.Enabled); err != nil {
		return CommandResponse{}, fmt.Errorf("set rule disabled: %w", err)
	}
	if err := uc.reloader.Reload(ctx); err != nil {
		return CommandResponse{}, fmt.Errorf("rule %q persisted, but registry reload failed: %w", id, err)
	}

	var r rule.Rule
	if builtin {
		r, _ = uc.builtins.Get(id)
	} else {
		got, err := uc.store.GetRule(ctx, id)
		if err != nil {
			return CommandResponse{}, fmt.Errorf("get rule: %w", err)
		}
		r = got
	}
	return CommandResponse{Rule: r, Builtin: builtin, Enabled: cmd.Enabled}, nil
}
