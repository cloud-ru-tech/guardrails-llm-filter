// Package rulesreload rebuilds the compiled rule registry from the built-in
// rules plus the custom/disabled sets persisted in the store, and atomically
// swaps the snapshot so the data path always reads a complete rule set. It
// backs both the boot-time merge and the periodic refresh that converges
// replicas on API changes made elsewhere; the rule mutation scenarios
// themselves live in internal/usecases/rules.
package rulesreload

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/registry"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
)

// Reloader owns the merge-and-swap path of the reloadable registry.
type Reloader struct {
	mu        sync.Mutex // serializes rebuilds so swaps are never torn
	fileRules []rule.Rule
	fileIDs   map[string]struct{}
	store     repository.RuleStore
	reg       *registry.Reloadable
}

// New creates the reloader. fileRules are the immutable built-in rules
// already registered in the initial registry snapshot behind reg.
func New(fileRules []rule.Rule, st repository.RuleStore, reg *registry.Reloadable) *Reloader {
	fileIDs := make(map[string]struct{}, len(fileRules))
	for _, r := range fileRules {
		fileIDs[r.ID] = struct{}{}
	}
	return &Reloader{
		fileRules: fileRules,
		fileIDs:   fileIDs,
		store:     st,
		reg:       reg,
	}
}

// Registry exposes the reloadable registry for data-path consumers.
func (r *Reloader) Registry() *registry.Reloadable { return r.reg }

// Reload merges built-in and stored custom rules, rebuilds the registry and
// swaps the snapshot. On store errors the current snapshot stays in place
// (fail-open: the service keeps serving the last known good rule set).
func (r *Reloader) Reload(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	custom, err := r.store.ListRules(ctx)
	if err != nil {
		return fmt.Errorf("list custom rules: %w", err)
	}
	disabledIDs, err := r.store.ListDisabledRuleIDs(ctx)
	if err != nil {
		return fmt.Errorf("list disabled rule ids: %w", err)
	}
	disabled := make(map[string]struct{}, len(disabledIDs))
	for _, id := range disabledIDs {
		disabled[id] = struct{}{}
	}

	merged := make([]rule.Rule, 0, len(r.fileRules)+len(custom))
	skipped := 0
	for _, fr := range r.fileRules {
		if _, off := disabled[fr.ID]; off {
			skipped++
			continue
		}
		merged = append(merged, fr)
	}
	for _, cr := range custom {
		if _, shadows := r.fileIDs[cr.ID]; shadows {
			// A stored rule must never shadow a built-in one; skip and warn.
			slog.WarnContext(ctx, "custom rule shadows a built-in rule_id, ignored", "rule_id", cr.ID)
			continue
		}
		if _, off := disabled[cr.ID]; off {
			skipped++
			continue
		}
		merged = append(merged, cr)
	}
	if skipped > 0 {
		slog.DebugContext(ctx, "excluded disabled rules from registry", "count", skipped)
	}

	reg, err := registry.Build(merged...)
	if err != nil {
		return fmt.Errorf("build rule registry: %w", err)
	}

	r.reg.Swap(reg)
	return nil
}

// RunRefresh periodically reloads rules from the store so replicas converge
// on API changes made elsewhere. interval <= 0 disables refreshing.
func (r *Reloader) RunRefresh(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.Reload(ctx); err != nil {
				slog.WarnContext(ctx, "rules refresh failed, keeping current registry", "error", err)
			}
		}
	}
}
