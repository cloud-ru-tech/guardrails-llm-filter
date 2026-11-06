package models_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
)

func TestParseAPIFormat(t *testing.T) {
	t.Parallel()

	for _, valid := range []string{"chat_completions", "MESSAGES", " messages ", "Responses", "decisions"} {
		_, err := models.ParseAPIFormat(valid)
		assert.NoError(t, err, valid)
	}
	for _, invalid := range []string{"", "chat", "openai", "completions", "/v1/messages"} {
		_, err := models.ParseAPIFormat(invalid)
		assert.Error(t, err, invalid)
	}
}

func defaultPaths() map[string]string {
	return map[string]string{
		"/v1/chat/completions": "chat_completions",
		"/v1/messages":         "messages",
		"/v1/responses":        "responses",
	}
}

func TestNewPathResolverValidation(t *testing.T) {
	t.Parallel()

	_, err := models.NewPathResolver(nil)
	assert.Error(t, err, "empty map must fail the boot")

	_, err = models.NewPathResolver(map[string]string{"v1/messages": "messages"})
	assert.Error(t, err, "path without leading slash")

	_, err = models.NewPathResolver(map[string]string{"/v1 /messages": "messages"})
	assert.Error(t, err, "path with whitespace")

	_, err = models.NewPathResolver(map[string]string{"/v1/foo": "not-a-format"})
	assert.Error(t, err, "unknown format name")
}

func TestPathResolverResolve(t *testing.T) {
	t.Parallel()
	r, err := models.NewPathResolver(defaultPaths())
	require.NoError(t, err)

	tests := []struct {
		path   string
		want   models.APIFormat
		wantOK bool
	}{
		{"/v1/chat/completions", models.APIFormatChatCompletions, true},
		{"/v1/messages", models.APIFormatMessages, true},
		{"/v1/responses", models.APIFormatResponses, true},
		// Proxy prefixes work via suffix matching with zero config.
		{"/openai/v1/chat/completions", models.APIFormatChatCompletions, true},
		{"/llm/gateway/v1/messages", models.APIFormatMessages, true},
		// Query strings are stripped before matching.
		{"/v1/chat/completions?api-version=2024-02-01", models.APIFormatChatCompletions, true},
		{"/proxy/v1/responses?stream=true", models.APIFormatResponses, true},
		// Suffix matches are segment-anchored by the leading slash of the key.
		{"/xv1/messages", "", false},
		// Suffix means suffix: trailing segments don't match.
		{"/v1/messages/foo", "", false},
		{"/v1/models", "", false},
		{"", "", false},
	}

	for _, tt := range tests {
		got, ok := r.Resolve(tt.path)
		assert.Equal(t, tt.wantOK, ok, tt.path)
		assert.Equal(t, tt.want, got, tt.path)
	}
}

func TestPathResolverLongestSuffixWins(t *testing.T) {
	t.Parallel()
	// A short key ("/completions") and a longer key that ends with it
	// ("/v1/chat/completions") mapped to different formats. The mapping is
	// contrived to exercise longest-suffix precedence, not a real deployment.
	r, err := models.NewPathResolver(map[string]string{
		"/completions":         "messages",
		"/v1/chat/completions": "chat_completions",
	})
	require.NoError(t, err)

	// Both keys are suffixes of the path; the longer, more specific one wins.
	got, ok := r.Resolve("/openai/v1/chat/completions")
	require.True(t, ok)
	assert.Equal(t, models.APIFormatChatCompletions, got)

	got, ok = r.Resolve("/api/completions")
	require.True(t, ok)
	assert.Equal(t, models.APIFormatMessages, got)
}

func TestDecisionsSuffixCoversBothVendorPaths(t *testing.T) {
	t.Parallel()
	// One GUARDRAILS_PATHS pair serves TypeSafe (/v1/decisions) and OpenRouter
	// (/api/alpha/decisions); the leading slash anchors it to a path segment.
	r, err := models.NewPathResolver(map[string]string{"/decisions": "decisions"})
	require.NoError(t, err)

	for _, path := range []string{"/decisions", "/v1/decisions", "/api/alpha/decisions", "/v1/decisions?x=1"} {
		got, ok := r.Resolve(path)
		assert.True(t, ok, path)
		assert.Equal(t, models.APIFormatDecisions, got, path)
	}
	_, ok := r.Resolve("/v1/xdecisions")
	assert.False(t, ok, "suffix must be segment-anchored")
}

func TestDecisionsIsOneWay(t *testing.T) {
	t.Parallel()
	assert.False(t, models.APIFormatDecisions.DemasksResponse())
	for _, f := range []models.APIFormat{models.APIFormatChatCompletions, models.APIFormatMessages, models.APIFormatResponses} {
		assert.True(t, f.DemasksResponse(), string(f))
	}
}
