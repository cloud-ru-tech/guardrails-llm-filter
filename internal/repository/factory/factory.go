// Package factory builds the configured repository.Store backend. It lives in a
// subpackage so the store interfaces stay import-cycle-free for the
// implementations.
package factory

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository/memory"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository/postgres"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository/redis"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository/statecodec"
)

// Backend identifies a store implementation.
type Backend string

const (
	BackendInMemory Backend = "in_memory"
	BackendRedis    Backend = "redis"
	BackendPostgres Backend = "postgres"
)

// Config selects and configures the persistence backend.
type Config struct {
	Backend    Backend
	MaskingTTL time.Duration

	// AuditTTL is the audit-record retention; AuditMaxEntries caps the
	// in-memory backend's audit map (0 = unlimited).
	AuditTTL        time.Duration
	AuditMaxEntries int

	Redis       redis.Config
	PostgresDSN string

	// EncryptionEnabled turns on AES-256-GCM encryption of masking state at
	// rest in external backends (no-op for in_memory: the data never leaves
	// the process). EncryptionKey is the standard base64 encoding of a
	// 32-byte key; it is validated at boot and never logged.
	EncryptionEnabled bool
	EncryptionKey     string
}

// New builds the configured Store. For external backends the connection is
// verified with Ping so misconfiguration fails at startup, not on the data
// path.
func New(ctx context.Context, cfg Config) (repository.Store, error) {
	// Validate the key whenever encryption is requested — even for in_memory —
	// so a misconfigured key fails at boot instead of hiding behind the
	// backend choice. The key material never appears in errors or logs.
	codec := statecodec.Plain()
	if cfg.EncryptionEnabled {
		var err error
		codec, err = statecodec.NewAESGCMFromBase64(cfg.EncryptionKey)
		if err != nil {
			return nil, fmt.Errorf("store encryption (GUARDRAILS_STORE_ENCRYPTION_KEY): %w", err)
		}
		if cfg.Backend == BackendInMemory || cfg.Backend == "" {
			slog.Info("store encryption is a no-op for the in_memory backend (data never leaves the process)")
		} else {
			slog.Info("masking-state store encryption enabled (aes256gcm)")
		}
	}

	switch cfg.Backend {
	case BackendInMemory, "":
		return memory.New(cfg.MaskingTTL, cfg.AuditTTL, cfg.AuditMaxEntries), nil

	case BackendRedis:
		s := redis.New(cfg.Redis, cfg.MaskingTTL, cfg.AuditTTL, codec)
		if err := s.Ping(ctx); err != nil {
			_ = s.Close()
			return nil, fmt.Errorf("redis store: %w", err)
		}
		return s, nil

	case BackendPostgres:
		s, err := postgres.New(ctx, cfg.PostgresDSN, cfg.MaskingTTL, cfg.AuditTTL, codec)
		if err != nil {
			return nil, fmt.Errorf("postgres store: %w", err)
		}
		return s, nil

	default:
		return nil, fmt.Errorf("unknown store backend %q (expected in_memory, redis or postgres)", cfg.Backend)
	}
}
