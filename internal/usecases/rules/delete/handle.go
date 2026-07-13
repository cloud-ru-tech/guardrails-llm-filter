package delete

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
	ruleerrors "github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/errors"
)

// Handle removes a custom rule, clears any lingering disabled flag, then
// reloads the registry.
func (uc *UseCase) Handle(ctx context.Context, cmd Command) (CommandResponse, error) {
	id := cmd.ID

	if uc.builtins.IsBuiltin(id) {
		return CommandResponse{}, fmt.Errorf("%w: %q", ruleerrors.ErrBuiltin, id)
	}
	if err := uc.store.DeleteRule(ctx, id); errors.Is(err, repository.ErrNotFound) {
		return CommandResponse{}, fmt.Errorf("%w: %q", ruleerrors.ErrNotFound, id)
	} else if err != nil {
		return CommandResponse{}, fmt.Errorf("delete rule: %w", err)
	}
	// Best-effort: drop a lingering disabled flag so re-creating the same ID
	// later does not resurrect it disabled.
	if err := uc.store.SetRuleDisabled(ctx, id, false); err != nil {
		slog.WarnContext(ctx, "failed to clear disabled flag for deleted rule", "rule_id", id, "error", err)
	}
	if err := uc.reloader.Reload(ctx); err != nil {
		return CommandResponse{}, fmt.Errorf("rule %q deleted, but registry reload failed: %w", id, err)
	}
	return CommandResponse{}, nil
}
