// Package statecodec serializes models.MaskingState for external store
// backends, optionally encrypting it at rest with AES-256-GCM.
//
// The encrypted form is a JSON envelope — valid JSON so the postgres JSONB
// column and the redis string value stay format-compatible:
//
//	{"_enc":"aes256gcm","v":1,"data":"<base64(nonce||ciphertext)>"}
//
// Envelope detection is by the "_enc" key (MaskingState JSON never contains
// it), not by byte prefix: JSONB normalizes key order and whitespace.
package statecodec

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
)

const (
	envelopeAlg     = "aes256gcm"
	envelopeVersion = 1

	// KeySize is the required decoded key length (AES-256).
	KeySize = 32
)

// envelope is the persisted form of an encrypted masking state.
// Data is nonce||ciphertext; encoding/json base64-encodes []byte.
type envelope struct {
	Enc  string `json:"_enc"`
	V    int    `json:"v"`
	Data []byte `json:"data"`
}

// Codec fuses (de)serialization and optional encryption of masking state.
// Decode failures on encrypted payloads wrap repository.ErrUndecryptable.
//
// EncryptString/DecryptString apply the same optional encryption to a single
// string (used for audit-record originals): the plain codec is pass-through,
// the AES codec produces/consumes the same JSON envelope as Encode/Decode.
type Codec interface {
	Encode(st models.MaskingState) ([]byte, error)
	Decode(payload []byte) (models.MaskingState, error)
	EncryptString(plaintext string) (string, error)
	DecryptString(value string) (string, error)
}

// Plain returns the pass-through codec: plain JSON, no encryption.
func Plain() Codec { return plainCodec{} }

type plainCodec struct{}

func (plainCodec) Encode(st models.MaskingState) ([]byte, error) {
	payload, err := json.Marshal(st)
	if err != nil {
		return nil, fmt.Errorf("marshal masking state: %w", err)
	}
	return payload, nil
}

func (plainCodec) Decode(payload []byte) (models.MaskingState, error) {
	if isEnvelope(payload) {
		// The record was written with encryption enabled and it is now
		// disabled. Surface a distinct error (not a silent not-found) so the
		// operator sees the misconfiguration; callers stay fail-open.
		return models.MaskingState{}, fmt.Errorf("%w: record is encrypted but store encryption is disabled", repository.ErrUndecryptable)
	}
	var st models.MaskingState
	if err := json.Unmarshal(payload, &st); err != nil {
		return models.MaskingState{}, fmt.Errorf("unmarshal masking state: %w", err)
	}
	return st, nil
}

// EncryptString is pass-through: the plain codec stores the value verbatim.
func (plainCodec) EncryptString(plaintext string) (string, error) { return plaintext, nil }

// DecryptString returns the value verbatim unless it is an encryption envelope
// written while encryption was enabled and now disabled — that is surfaced as
// ErrUndecryptable so callers drop it rather than leaking ciphertext.
func (plainCodec) DecryptString(value string) (string, error) {
	if isEnvelope([]byte(value)) {
		return "", fmt.Errorf("%w: value is encrypted but store encryption is disabled", repository.ErrUndecryptable)
	}
	return value, nil
}

// NewAESGCM returns an encrypting codec keyed with a 32-byte AES-256 key.
// Its Decode transparently accepts legacy plaintext records (rolling enable).
func NewAESGCM(key []byte) (Codec, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("encryption key must be %d bytes, got %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("init aes cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("init aes-gcm: %w", err)
	}
	return &aesCodec{aead: aead}, nil
}

// NewAESGCMFromBase64 builds the AES-256-GCM codec from a standard
// base64-encoded 32-byte key (e.g. `openssl rand -base64 32`). Error messages
// never include the key material.
func NewAESGCMFromBase64(key string) (Codec, error) {
	if key == "" {
		return nil, fmt.Errorf("encryption key is empty")
	}
	raw, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		return nil, fmt.Errorf("encryption key is not valid base64")
	}
	return NewAESGCM(raw)
}

type aesCodec struct {
	aead cipher.AEAD
}

func (c *aesCodec) Encode(st models.MaskingState) ([]byte, error) {
	plain, err := json.Marshal(st)
	if err != nil {
		return nil, fmt.Errorf("marshal masking state: %w", err)
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	sealed := c.aead.Seal(nonce, nonce, plain, nil)
	payload, err := json.Marshal(envelope{Enc: envelopeAlg, V: envelopeVersion, Data: sealed})
	if err != nil {
		return nil, fmt.Errorf("marshal envelope: %w", err)
	}
	return payload, nil
}

func (c *aesCodec) Decode(payload []byte) (models.MaskingState, error) {
	if !isEnvelope(payload) {
		// Legacy plaintext record written before encryption was enabled.
		var st models.MaskingState
		if err := json.Unmarshal(payload, &st); err != nil {
			return models.MaskingState{}, fmt.Errorf("unmarshal masking state: %w", err)
		}
		return st, nil
	}
	var env envelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return models.MaskingState{}, fmt.Errorf("%w: malformed envelope", repository.ErrUndecryptable)
	}
	if env.Enc != envelopeAlg || env.V != envelopeVersion {
		return models.MaskingState{}, fmt.Errorf("%w: unsupported envelope alg %q version %d", repository.ErrUndecryptable, env.Enc, env.V)
	}
	if len(env.Data) < c.aead.NonceSize() {
		return models.MaskingState{}, fmt.Errorf("%w: envelope data too short", repository.ErrUndecryptable)
	}
	nonce, ciphertext := env.Data[:c.aead.NonceSize()], env.Data[c.aead.NonceSize():]
	plain, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		// Wrong key or tampered record; the AEAD error carries no secrets.
		return models.MaskingState{}, fmt.Errorf("%w: %v", repository.ErrUndecryptable, err)
	}
	var st models.MaskingState
	if err := json.Unmarshal(plain, &st); err != nil {
		return models.MaskingState{}, fmt.Errorf("%w: decrypted payload is not a masking state", repository.ErrUndecryptable)
	}
	return st, nil
}

// EncryptString seals a single string into the same JSON envelope Encode uses.
func (c *aesCodec) EncryptString(plaintext string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	payload, err := json.Marshal(envelope{Enc: envelopeAlg, V: envelopeVersion, Data: sealed})
	if err != nil {
		return "", fmt.Errorf("marshal envelope: %w", err)
	}
	return string(payload), nil
}

// DecryptString opens an envelope produced by EncryptString. A value that is
// not an envelope is returned verbatim (legacy plaintext written before
// encryption was enabled — rolling enable, mirroring Decode).
func (c *aesCodec) DecryptString(value string) (string, error) {
	payload := []byte(value)
	if !isEnvelope(payload) {
		return value, nil
	}
	var env envelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return "", fmt.Errorf("%w: malformed envelope", repository.ErrUndecryptable)
	}
	if env.Enc != envelopeAlg || env.V != envelopeVersion {
		return "", fmt.Errorf("%w: unsupported envelope alg %q version %d", repository.ErrUndecryptable, env.Enc, env.V)
	}
	if len(env.Data) < c.aead.NonceSize() {
		return "", fmt.Errorf("%w: envelope data too short", repository.ErrUndecryptable)
	}
	nonce, ciphertext := env.Data[:c.aead.NonceSize()], env.Data[c.aead.NonceSize():]
	plain, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("%w: %v", repository.ErrUndecryptable, err)
	}
	return string(plain), nil
}

// isEnvelope reports whether the payload is an encryption envelope, detected
// by the presence of the "_enc" marker key.
func isEnvelope(payload []byte) bool {
	var probe struct {
		Enc string `json:"_enc"`
	}
	return json.Unmarshal(payload, &probe) == nil && probe.Enc != ""
}
