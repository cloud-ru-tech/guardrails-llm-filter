package create

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
	ruleerrors "github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/errors"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/validation"
)

// Handle validates and persists a new custom rule, then reloads the registry.
func (uc *UseCase) Handle(ctx context.Context, cmd Command) (CommandResponse, error) {
	r := cmd.Rule

	if err := validation.Validate(r, uc.limits); err != nil {
		return CommandResponse{}, err
	}
	if uc.builtins.IsBuiltin(r.ID) {
		return CommandResponse{}, fmt.Errorf("%w: %q", ruleerrors.ErrAlreadyExists, r.ID)
	}

	// Enforce the custom-rule count limit before the write. This is a
	// best-effort guard (a benign race between concurrent creators may allow a
	// few over the limit), which is acceptable for a DoS ceiling.
	if uc.maxCustom > 0 {
		existing, err := uc.store.ListRules(ctx)
		if err != nil {
			return CommandResponse{}, fmt.Errorf("count custom rules: %w", err)
		}
		if len(existing) >= uc.maxCustom {
			return CommandResponse{}, fmt.Errorf("%w: limit is %d", ruleerrors.ErrTooManyRules, uc.maxCustom)
		}
	}

	// CreateRule is atomic create-if-absent: two concurrent creators of the
	// same id cannot both succeed (no TOCTOU between existence check and save).
	if err := uc.store.CreateRule(ctx, r); errors.Is(err, repository.ErrAlreadyExists) {
		return CommandResponse{}, fmt.Errorf("%w: %q", ruleerrors.ErrAlreadyExists, r.ID)
	} else if err != nil {
		return CommandResponse{}, fmt.Errorf("create rule: %w", err)
	}

	// Clear any lingering disabled flag for this ID. The disabled set has no
	// foreign key to rules, so a flag left behind by a prior delete/setenabled
	// on the same ID would make the reloader silently exclude this fresh rule —
	// the API would report it enabled while the data path never scans it.
	// Best-effort (like delete's clear): a failure is logged, and the response's
	// Enabled below reflects the real state so the mismatch is never hidden.
	if err := uc.store.SetRuleDisabled(ctx, r.ID, false); err != nil {
		slog.WarnContext(ctx, "failed to clear disabled flag for created rule", "rule_id", r.ID, "error", err)
	}

	if err := uc.reloader.Reload(ctx); err != nil {
		return CommandResponse{}, fmt.Errorf("rule %q persisted, but registry reload failed: %w", r.ID, err)
	}

	// Report the rule's true enabled state rather than assuming enabled: if the
	// disabled-flag clear above failed, the reloader excluded the rule and the
	// response must surface that instead of a misleading enabled:true.
	enabled := true
	if disabled, err := uc.store.ListDisabledRuleIDs(ctx); err != nil {
		slog.WarnContext(ctx, "rule created, but reading disabled state for the response failed",
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
