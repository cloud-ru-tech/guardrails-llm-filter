package postgres_test

import (
	"context"
	"crypto/rand"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
	pgrepo "github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository/postgres"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository/repositorytest"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository/statecodec"
)

// TestConformance spins up a disposable PostgreSQL container shared by the
// plain and encrypted sub-suites. It is skipped in -short mode and when
// Docker is unavailable.
func TestConformance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping postgres store test in -short mode")
	}

	ctx := context.Background()
	dsn := startPostgres(t, ctx)

	newStore := func(codec statecodec.Codec) func(t *testing.T) repository.Store {
		return func(t *testing.T) repository.Store {
			s, err := pgrepo.New(ctx, dsn, 15*time.Minute, 24*time.Hour, codec)
			require.NoError(t, err)
			truncateAll(t, ctx, dsn) // isolate sub-tests sharing one container
			t.Cleanup(func() { require.NoError(t, s.Close()) })
			return s
		}
	}

	t.Run("plain", func(t *testing.T) {
		repositorytest.Run(t, newStore(statecodec.Plain()), repositorytest.Options{})
	})

	// The encrypting codec must be transparent to every store contract.
	t.Run("encrypted", func(t *testing.T) {
		repositorytest.Run(t, newStore(testCodec(t)), repositorytest.Options{})
	})

	t.Run("encryption at rest", func(t *testing.T) {
		s, err := pgrepo.New(ctx, dsn, 15*time.Minute, 24*time.Hour, testCodec(t))
		require.NoError(t, err)
		truncateAll(t, ctx, dsn)
		t.Cleanup(func() { require.NoError(t, s.Close()) })

		st := models.MaskingState{
			TriggeredRuleIDs:   []string{"email"},
			TriggeredDataTypes: []models.DataType{models.DataTypePERSONALDATA},
			Replacements: []models.Replacement{
				{RuleID: "email", Original: "user@example.com", Placeholder: "<EMAIL_1>"},
			},
		}
		require.NoError(t, s.PutMaskingState(ctx, "req-1", st))

		// Raw column content: the envelope must survive JSONB normalization
		// and must not leak the original value.
		pool, err := pgxpool.New(ctx, dsn)
		require.NoError(t, err)
		defer pool.Close()
		var raw string
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT state::text FROM guardrails_masking_state WHERE request_id = 'req-1'`).Scan(&raw))
		assert.NotContains(t, raw, "user@example.com", "original must not be stored in plaintext")
		assert.Contains(t, raw, `"_enc"`)
		assert.Contains(t, raw, `aes256gcm`)

		// And the round trip still works through the JSONB column.
		got, err := s.GetMaskingState(ctx, "req-1")
		require.NoError(t, err)
		assert.Equal(t, st, got)
	})
}

func testCodec(t *testing.T) statecodec.Codec {
	t.Helper()
	key := make([]byte, statecodec.KeySize)
	_, err := rand.Read(key)
	require.NoError(t, err)
	codec, err := statecodec.NewAESGCM(key)
	require.NoError(t, err)
	return codec
}

func startPostgres(t *testing.T, ctx context.Context) string {
	t.Helper()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("guardrails"),
		tcpostgres.WithUsername("guardrails"),
		tcpostgres.WithPassword("guardrails"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Skipf("cannot start postgres container (is Docker running?): %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	return dsn
}

func truncateAll(t *testing.T, ctx context.Context, dsn string) {
	t.Helper()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()
	_, err = pool.Exec(ctx,
		`TRUNCATE guardrails_rules, guardrails_disabled_rules, guardrails_settings, guardrails_masking_state, guardrails_audit`)
	require.NoError(t, err)
}
