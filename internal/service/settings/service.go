// Package settings resolves the global guardrails settings and the
// per-request narrow-only header override. It replaces the per-project
// settings resolution against the managed control plane.
package settings

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync/atomic"
	"time"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
)

// Service holds an in-process cached copy of the global settings backed by
// a SettingsStore. Reads are lock-free; updates persist to the store first
// and then swap the cache.
type Service struct {
	store    repository.SettingsStore
	defaults models.GuardrailsSettings
	cur      atomic.Pointer[models.GuardrailsSettings]
}

// New creates the service with env-derived defaults. Call Load before
// serving traffic.
func New(st repository.SettingsStore, defaults models.GuardrailsSettings) *Service {
	s := &Service{store: st, defaults: cloneSettings(defaults)}
	d := cloneSettings(defaults)
	s.cur.Store(&d)
	return s
}

// Load initialises the cache from the repository. When the store has no
// persisted settings yet, the env defaults are written there so the
// configuration API always reads and writes the same source of truth.
// A store error is not fatal: the service starts on env defaults and the
// refresh ticker heals the cache later (fail-open).
func (s *Service) Load(ctx context.Context) error {
	stored, err := s.store.GetSettings(ctx)
	if err != nil {
		return fmt.Errorf("load guardrails settings: %w", err)
	}

	if stored == nil {
		// Seed-once: only the first writer persists env defaults. A concurrent
		// replica or an API update racing this boot must not be clobbered, so
		// on a lost race we re-read and adopt the winner's value.
		written, err := s.store.SaveSettingsIfAbsent(ctx, s.defaults)
		if err != nil {
			return fmt.Errorf("seed guardrails settings: %w", err)
		}
		if written {
			return nil // cache already holds the defaults
		}
		stored, err = s.store.GetSettings(ctx)
		if err != nil {
			return fmt.Errorf("re-read guardrails settings after seed race: %w", err)
		}
		if stored == nil {
			return nil // vanished (TTL/deletion); keep env defaults in cache
		}
	}

	cp := cloneSettings(*stored)
	s.cur.Store(&cp)
	return nil
}

// Global returns the cached global settings (lock-free).
func (s *Service) Global() models.GuardrailsSettings {
	return *s.cur.Load()
}

// Update persists new settings (normalized) and swaps the cache.
func (s *Service) Update(ctx context.Context, gs models.GuardrailsSettings) error {
	cp := cloneSettings(gs)
	if err := s.store.SaveSettings(ctx, cp); err != nil {
		return fmt.Errorf("save guardrails settings: %w", err)
	}
	s.cur.Store(&cp)
	return nil
}

// Refresh re-reads the store, converging replicas that did not receive an
// API update directly. Store errors keep the current cache (fail-open).
func (s *Service) Refresh(ctx context.Context) error {
	stored, err := s.store.GetSettings(ctx)
	if err != nil {
		return fmt.Errorf("refresh guardrails settings: %w", err)
	}
	if stored == nil {
		return nil
	}
	cp := cloneSettings(*stored)
	s.cur.Store(&cp)
	return nil
}

// RunRefresh periodically calls Refresh until ctx is done. interval <= 0
// disables refreshing. Intended to run in its own goroutine.
func (s *Service) RunRefresh(ctx context.Context, interval time.Duration) {
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
			if err := s.Refresh(ctx); err != nil {
				slog.WarnContext(ctx, "settings refresh failed, keeping cached settings", "error", err)
			}
		}
	}
}

func cloneSettings(gs models.GuardrailsSettings) models.GuardrailsSettings {
	gs.DataTypes = slices.Clone(gs.DataTypes)
	if !gs.Mode.IsValid() {
		// Settings persisted before the mode field existed (or hand-edited
		// garbage) normalize to enforce — fail toward more protection.
		gs.Mode = models.ModeEnforce
	}
	return gs
}
