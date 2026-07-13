package audit

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/config"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository/statecodec"
)

func TestRecordResponseGating(t *testing.T) {
	t.Run("disabled is a no-op", func(t *testing.T) {
		st := &fakeAuditStore{}
		r := New(st, testResolver(), false, false, config.OriginalsOff, nil)
		r.Record(testMetadata(), testState(), nil)
		waitRecorded(t, st)

		r.RecordResponse("req-1", []string{"masked <EMAIL_1>"})
		// Give any (erroneously spawned) goroutine a chance to run.
		time.Sleep(50 * time.Millisecond)
		assert.Nil(t, st.last(t).MaskedResponseTexts)
	})

	t.Run("enabled persists response texts", func(t *testing.T) {
		st := &fakeAuditStore{}
		r := New(st, testResolver(), false, true, config.OriginalsOff, nil)
		r.Record(testMetadata(), testState(), nil)
		waitRecorded(t, st)

		r.RecordResponse("req-1", []string{"masked <EMAIL_1>"})
		require.Eventually(t, func() bool {
			return len(st.last(t).MaskedResponseTexts) == 1
		}, time.Second, 5*time.Millisecond)
		assert.Equal(t, []string{"masked <EMAIL_1>"}, st.last(t).MaskedResponseTexts)
	})

	t.Run("empty request id is a no-op", func(t *testing.T) {
		st := &fakeAuditStore{}
		r := New(st, testResolver(), false, true, config.OriginalsOff, nil)
		r.RecordResponse("", []string{"x"})
		time.Sleep(50 * time.Millisecond)
		assert.Zero(t, st.count())
	})
}

func TestBuildRecordOriginals(t *testing.T) {
	const plaintext = "user@example.com"

	t.Run("off omits originals", func(t *testing.T) {
		r := New(&fakeAuditStore{}, testResolver(), false, false, config.OriginalsOff, nil)
		rec := r.buildRecord(testMetadata(), testState(), nil)
		require.Len(t, rec.Replacements, 1)
		assert.Empty(t, rec.Replacements[0].Original)
	})

	t.Run("plain stores the raw original", func(t *testing.T) {
		r := New(&fakeAuditStore{}, testResolver(), false, false, config.OriginalsPlain, nil)
		rec := r.buildRecord(testMetadata(), testState(), nil)
		require.Len(t, rec.Replacements, 1)
		assert.Equal(t, plaintext, rec.Replacements[0].Original)
	})

	t.Run("encrypted seals the original, decryptable with the key", func(t *testing.T) {
		codec, err := statecodec.NewAESGCM(make([]byte, statecodec.KeySize))
		require.NoError(t, err)
		r := New(&fakeAuditStore{}, testResolver(), false, false, config.OriginalsEncrypted, codec)

		rec := r.buildRecord(testMetadata(), testState(), nil)
		require.Len(t, rec.Replacements, 1)
		sealed := rec.Replacements[0].Original
		assert.NotEmpty(t, sealed)
		assert.NotEqual(t, plaintext, sealed) // not stored in the clear

		opened, err := codec.DecryptString(sealed)
		require.NoError(t, err)
		assert.Equal(t, plaintext, opened)
	})
}
