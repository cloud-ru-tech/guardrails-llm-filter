package update

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
	ruleerrors "github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/errors"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/validation"
)

// Handle validates and replaces an existing custom rule, then reloads the
// registry. The response reflects the rule's current enabled state.
func (uc *UseCase) Handle(ctx context.Context, cmd Command) (CommandResponse, error) {
	r := cmd.Rule

	if uc.builtins.IsBuiltin(r.ID) {
		return CommandResponse{}, fmt.Errorf("%w: %q", ruleerrors.ErrBuiltin, r.ID)
	}
	if err := validation.Validate(r, uc.limits); err != nil {
		return CommandResponse{}, err
	}
	if _, err := uc.store.GetRule(ctx, r.ID); errors.Is(err, repository.ErrNotFound) {
		return CommandResponse{}, fmt.Errorf("%w: %q", ruleerrors.ErrNotFound, r.ID)
	} else if err != nil {
		return CommandResponse{}, fmt.Errorf("check rule existence: %w", err)
	}

	if err := uc.store.SaveRule(ctx, r); err != nil {
		return CommandResponse{}, fmt.Errorf("save rule: %w", err)
	}
	if err := uc.reloader.Reload(ctx); err != nil {
		return CommandResponse{}, fmt.Errorf("rule %q persisted, but registry reload failed: %w", r.ID, err)
	}

	// The mutation has already been persisted and the registry reloaded, so
	// the update succeeded. The disabled-state lookup only decorates the
	// response's Enabled flag; if it fails, report success (assuming enabled)
	// rather than a misleading 500.
	enabled := true
	if disabled, err := uc.store.ListDisabledRuleIDs(ctx); err != nil {
		slog.WarnContext(ctx, "rule updated, but reading disabled state for the response failed",
			"rule_id", r.ID, "error", err)
	} else {
		for _, id := range disabled {
			if id == r.ID {
				enabled = false
				break
			}
		}
	}
	return CommandResponse{Rule: r, Enabled: enabled}, nil
}
