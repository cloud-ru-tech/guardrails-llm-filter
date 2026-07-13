package factory_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository/factory"
)

func validKey(t *testing.T) string {
	t.Helper()
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(key)
}

func TestNewEncryptionKeyValidation(t *testing.T) {
	ctx := context.Background()
	base := factory.Config{
		Backend:    factory.BackendInMemory,
		MaskingTTL: time.Minute,
		AuditTTL:   time.Minute,
	}

	t.Run("disabled ignores key", func(t *testing.T) {
		cfg := base
		s, err := factory.New(ctx, cfg)
		require.NoError(t, err)
		require.NoError(t, s.Close())
	})

	t.Run("enabled with valid key", func(t *testing.T) {
		cfg := base
		cfg.EncryptionEnabled = true
		cfg.EncryptionKey = validKey(t)
		s, err := factory.New(ctx, cfg)
		require.NoError(t, err)
		require.NoError(t, s.Close())
	})

	for name, key := range map[string]string{
		"empty key":      "",
		"not base64":     "%%%not-base64%%%",
		"short key":      base64.StdEncoding.EncodeToString(make([]byte, 16)),
		"oversized  key": base64.StdEncoding.EncodeToString(make([]byte, 48)),
	} {
		t.Run(name, func(t *testing.T) {
			cfg := base
			cfg.EncryptionEnabled = true
			cfg.EncryptionKey = key
			_, err := factory.New(ctx, cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "GUARDRAILS_STORE_ENCRYPTION_KEY")
			if key != "" {
				assert.NotContains(t, err.Error(), key, "error must not leak key material")
			}
		})
	}
}
