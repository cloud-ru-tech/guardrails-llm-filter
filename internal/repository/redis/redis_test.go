package redis_test

import (
	"crypto/rand"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
	redisrepo "github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository/redis"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository/repositorytest"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository/statecodec"
)

const (
	maskingTTL = 15 * time.Minute
	auditTTL   = 24 * time.Hour
)

func testCodec(t *testing.T) statecodec.Codec {
	t.Helper()
	key := make([]byte, statecodec.KeySize)
	_, err := rand.Read(key)
	require.NoError(t, err)
	codec, err := statecodec.NewAESGCM(key)
	require.NoError(t, err)
	return codec
}

func runConformance(t *testing.T, codec statecodec.Codec) {
	var current *miniredis.Miniredis

	repositorytest.Run(t, func(t *testing.T) repository.Store {
		mr := miniredis.RunT(t)
		current = mr
		s := redisrepo.New(redisrepo.Config{Addr: mr.Addr()}, maskingTTL, auditTTL, codec)
		t.Cleanup(func() { require.NoError(t, s.Close()) })
		return s
	}, repositorytest.Options{
		ExpireMaskingState: func(t *testing.T) {
			current.FastForward(maskingTTL + time.Second)
		},
		ExpireAudit: func(t *testing.T) {
			current.FastForward(auditTTL + time.Second)
		},
	})
}

func TestConformance(t *testing.T) {
	runConformance(t, statecodec.Plain())
}

// TestConformanceEncrypted runs the same suite with at-rest encryption on:
// the codec must be transparent to every store contract.
func TestConformanceEncrypted(t *testing.T) {
	runConformance(t, testCodec(t))
}

func sampleState() models.MaskingState {
	return models.MaskingState{
		TriggeredRuleIDs:   []string{"email"},
		TriggeredDataTypes: []models.DataType{models.DataTypePERSONALDATA},
		Replacements: []models.Replacement{
			{RuleID: "email", Original: "user@example.com", Placeholder: "<EMAIL_1>"},
		},
	}
}

func TestEncryptionAtRest(t *testing.T) {
	ctx := t.Context()
	mr := miniredis.RunT(t)
	s := redisrepo.New(redisrepo.Config{Addr: mr.Addr()}, maskingTTL, auditTTL, testCodec(t))
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	require.NoError(t, s.PutMaskingState(ctx, "req-1", sampleState()))

	raw, err := mr.Get("guardrails:mask:req-1")
	require.NoError(t, err)
	assert.NotContains(t, raw, "user@example.com", "original must not be stored in plaintext")
	assert.Contains(t, raw, `"_enc":"aes256gcm"`)
}

func TestLegacyPlaintextRead(t *testing.T) {
	ctx := t.Context()
	mr := miniredis.RunT(t)
	codec := testCodec(t)

	// A plaintext record written before encryption was enabled.
	plain := redisrepo.New(redisrepo.Config{Addr: mr.Addr()}, maskingTTL, auditTTL, statecodec.Plain())
	require.NoError(t, plain.PutMaskingState(ctx, "req-legacy", sampleState()))
	require.NoError(t, plain.Close())

	encrypted := redisrepo.New(redisrepo.Config{Addr: mr.Addr()}, maskingTTL, auditTTL, codec)
	t.Cleanup(func() { require.NoError(t, encrypted.Close()) })
	got, err := encrypted.GetMaskingState(ctx, "req-legacy")
	require.NoError(t, err)
	assert.Equal(t, sampleState(), got)
}

func TestEncryptedRecordWithEncryptionDisabled(t *testing.T) {
	ctx := t.Context()
	mr := miniredis.RunT(t)

	encrypted := redisrepo.New(redisrepo.Config{Addr: mr.Addr()}, maskingTTL, auditTTL, testCodec(t))
	require.NoError(t, encrypted.PutMaskingState(ctx, "req-1", sampleState()))
	require.NoError(t, encrypted.Close())

	plain := redisrepo.New(redisrepo.Config{Addr: mr.Addr()}, maskingTTL, auditTTL, statecodec.Plain())
	t.Cleanup(func() { require.NoError(t, plain.Close()) })
	_, err := plain.GetMaskingState(ctx, "req-1")
	require.ErrorIs(t, err, repository.ErrUndecryptable)
}
