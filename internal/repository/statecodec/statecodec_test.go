package statecodec

import (
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
)

func sampleState() models.MaskingState {
	return models.MaskingState{
		TriggeredRuleIDs:   []string{"email", "phone_ru"},
		TriggeredDataTypes: []models.DataType{models.DataTypePERSONALDATA, models.DataTypeCUSTOM},
		Replacements: []models.Replacement{
			{RuleID: "email", Original: "user@example.com", Placeholder: "<EMAIL_1>"},
			{RuleID: "phone_ru", Original: "+7 999 123-45-67", Placeholder: "<PHONE_2>"},
		},
	}
}

func testKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, KeySize)
	_, err := rand.Read(key)
	require.NoError(t, err)
	return key
}

func TestAESGCMRoundTrip(t *testing.T) {
	codec, err := NewAESGCM(testKey(t))
	require.NoError(t, err)

	st := sampleState()
	payload, err := codec.Encode(st)
	require.NoError(t, err)

	assert.Contains(t, string(payload), `"_enc":"aes256gcm"`)
	assert.NotContains(t, string(payload), "user@example.com")
	assert.NotContains(t, string(payload), "<EMAIL_1>")

	got, err := codec.Decode(payload)
	require.NoError(t, err)
	assert.Equal(t, st, got)
}

func TestAESGCMEncodeIsNonDeterministic(t *testing.T) {
	codec, err := NewAESGCM(testKey(t))
	require.NoError(t, err)

	a, err := codec.Encode(sampleState())
	require.NoError(t, err)
	b, err := codec.Encode(sampleState())
	require.NoError(t, err)
	assert.NotEqual(t, a, b, "random nonce must make ciphertexts differ")
}

func TestAESGCMDecodeTamperedPayload(t *testing.T) {
	codec, err := NewAESGCM(testKey(t))
	require.NoError(t, err)

	payload, err := codec.Encode(sampleState())
	require.NoError(t, err)

	// Flip one base64 character inside the data field.
	tampered := []byte(string(payload))
	i := len(tampered) - 10
	if tampered[i] == 'A' {
		tampered[i] = 'B'
	} else {
		tampered[i] = 'A'
	}
	_, err = codec.Decode(tampered)
	require.ErrorIs(t, err, repository.ErrUndecryptable)
}

func TestAESGCMDecodeWrongKey(t *testing.T) {
	enc, err := NewAESGCM(testKey(t))
	require.NoError(t, err)
	dec, err := NewAESGCM(testKey(t))
	require.NoError(t, err)

	payload, err := enc.Encode(sampleState())
	require.NoError(t, err)
	_, err = dec.Decode(payload)
	require.ErrorIs(t, err, repository.ErrUndecryptable)
}

func TestAESGCMDecodeLegacyPlaintext(t *testing.T) {
	codec, err := NewAESGCM(testKey(t))
	require.NoError(t, err)

	plain, err := Plain().Encode(sampleState())
	require.NoError(t, err)

	got, err := codec.Decode(plain)
	require.NoError(t, err)
	assert.Equal(t, sampleState(), got)
}

func TestAESGCMDecodeGarbage(t *testing.T) {
	codec, err := NewAESGCM(testKey(t))
	require.NoError(t, err)

	for name, payload := range map[string][]byte{
		"not json":        []byte("not json at all"),
		"short data":      []byte(`{"_enc":"aes256gcm","v":1,"data":"AAAA"}`),
		"unsupported alg": []byte(`{"_enc":"xchacha20","v":1,"data":"AAAA"}`),
		"bad version":     []byte(`{"_enc":"aes256gcm","v":2,"data":"AAAA"}`),
		"bad data base64": []byte(`{"_enc":"aes256gcm","v":1,"data":"!!!"}`),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := codec.Decode(payload)
			require.Error(t, err)
			if name != "not json" {
				// Envelope-shaped payloads must carry the sentinel.
				require.ErrorIs(t, err, repository.ErrUndecryptable)
			}
		})
	}
}

func TestPlainRoundTrip(t *testing.T) {
	payload, err := Plain().Encode(sampleState())
	require.NoError(t, err)
	got, err := Plain().Decode(payload)
	require.NoError(t, err)
	assert.Equal(t, sampleState(), got)
}

func TestPlainDecodeEnvelope(t *testing.T) {
	codec, err := NewAESGCM(testKey(t))
	require.NoError(t, err)
	payload, err := codec.Encode(sampleState())
	require.NoError(t, err)

	_, err = Plain().Decode(payload)
	require.ErrorIs(t, err, repository.ErrUndecryptable)
}

func TestNewAESGCMKeyValidation(t *testing.T) {
	_, err := NewAESGCM(make([]byte, 16))
	require.Error(t, err)
	_, err = NewAESGCM(nil)
	require.Error(t, err)
}

func TestNewAESGCMFromBase64(t *testing.T) {
	key := testKey(t)
	encoded := base64.StdEncoding.EncodeToString(key)

	codec, err := NewAESGCMFromBase64(encoded)
	require.NoError(t, err)
	require.NotNil(t, codec)

	for name, bad := range map[string]string{
		"empty":      "",
		"not base64": "%%%not-base64%%%",
		"16 bytes":   base64.StdEncoding.EncodeToString(make([]byte, 16)),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewAESGCMFromBase64(bad)
			require.Error(t, err)
			if bad != "" {
				assert.NotContains(t, err.Error(), bad, "error must not leak key material")
			}
		})
	}
}
