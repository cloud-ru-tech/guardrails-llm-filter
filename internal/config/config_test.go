package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/config"
)

func TestLoadDefaultPaths(t *testing.T) {
	cfg, err := config.Load()
	require.NoError(t, err)

	assert.Equal(t, map[string]string{
		"/v1/chat/completions": "chat_completions",
		"/v1/messages":         "messages",
		"/v1/responses":        "responses",
	}, cfg.Guardrails.Paths)
}

func TestLoadDefaultDataTypesIncludesCustom(t *testing.T) {
	cfg, err := config.Load()
	require.NoError(t, err)
	// 6 (CUSTOM) must be enabled by default, otherwise custom rules created via
	// the API silently never scan.
	assert.Equal(t, "1,2,3,4,5,6", cfg.Guardrails.DataTypes)
}

func TestLoadPathsOverrideMergesOverDefaults(t *testing.T) {
	t.Setenv("GUARDRAILS_PATHS", "/llm/chat:chat_completions")

	cfg, err := config.Load()
	require.NoError(t, err)
	// A partial override must MERGE on top of the built-in core paths so it
	// can never silently disable masking for a core endpoint.
	assert.Equal(t, map[string]string{
		"/llm/chat":            "chat_completions",
		"/v1/chat/completions": "chat_completions",
		"/v1/messages":         "messages",
		"/v1/responses":        "responses",
	}, cfg.Guardrails.Paths)
}

func TestLoadPathsOverrideForCorePathWins(t *testing.T) {
	// A user entry for a core path overrides the default format; the other
	// core paths are still merged in.
	t.Setenv("GUARDRAILS_PATHS", "/v1/messages:chat_completions")

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, "chat_completions", cfg.Guardrails.Paths["/v1/messages"])
	assert.Equal(t, "chat_completions", cfg.Guardrails.Paths["/v1/chat/completions"])
	assert.Len(t, cfg.Guardrails.Paths, 3)
}

func TestLoadPathsInvalidFormatFailsBoot(t *testing.T) {
	t.Setenv("GUARDRAILS_PATHS", "/v1/chat:not-a-format")

	_, err := config.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GUARDRAILS_PATHS")
}

func TestLoadPathsWithoutSlashFailsBoot(t *testing.T) {
	t.Setenv("GUARDRAILS_PATHS", "v1/chat:chat_completions")

	_, err := config.Load()
	require.Error(t, err)
}

func TestLoadNegativeMaskParallelMinBytesFailsBoot(t *testing.T) {
	t.Setenv("GUARDRAILS_MASK_PARALLEL_MIN_BYTES", "-1")

	_, err := config.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GUARDRAILS_MASK_PARALLEL_MIN_BYTES")
}

func TestLoadZeroMaskParallelMinBytesUsesDefault(t *testing.T) {
	t.Setenv("GUARDRAILS_MASK_PARALLEL_MIN_BYTES", "0")

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, 0, cfg.Guardrails.MaskParallelMinBytes)
}

func TestLoadListenAddr(t *testing.T) {
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, ":8080", cfg.ListenAddr)

	t.Setenv("GUARDRAILS_LISTEN_ADDR", ":9999")
	cfg, err = config.Load()
	require.NoError(t, err)
	assert.Equal(t, ":9999", cfg.ListenAddr)
}

func TestLoadUpstreamDefaults(t *testing.T) {
	cfg, err := config.Load()
	require.NoError(t, err)
	// BaseURL is optional until the gateway is the data-plane server.
	assert.Empty(t, cfg.Upstream.BaseURL)
	assert.Equal(t, 120*time.Second, cfg.Upstream.Timeout)
	assert.Equal(t, 100, cfg.Upstream.MaxIdleConns)
	assert.Equal(t, 100, cfg.Upstream.MaxIdleConnsPerHost)
	assert.Equal(t, 90*time.Second, cfg.Upstream.IdleConnTimeout)
	assert.False(t, cfg.Upstream.InsecureSkipVerify)
	assert.Empty(t, cfg.Upstream.PathBaseURLs)
}

func TestLoadUpstreamFromEnv(t *testing.T) {
	t.Setenv("GUARDRAILS_UPSTREAM_BASE_URL", "https://api.openai.com")
	t.Setenv("GUARDRAILS_UPSTREAM_TIMEOUT", "30s")
	t.Setenv("GUARDRAILS_UPSTREAM_MAX_IDLE_CONNS", "10")
	t.Setenv("GUARDRAILS_UPSTREAM_INSECURE_SKIP_VERIFY", "true")

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, "https://api.openai.com", cfg.Upstream.BaseURL)
	assert.Equal(t, 30*time.Second, cfg.Upstream.Timeout)
	assert.Equal(t, 10, cfg.Upstream.MaxIdleConns)
	assert.True(t, cfg.Upstream.InsecureSkipVerify)
}

func TestLoadUpstreamPathBaseURLsUsesEqualsSeparator(t *testing.T) {
	// '=' separates key/value because URLs contain ':'.
	t.Setenv("GUARDRAILS_UPSTREAM_PATH_BASE_URLS",
		"/v1/messages=https://api.anthropic.com,/v1/responses=https://api.openai.com")

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"/v1/messages":  "https://api.anthropic.com",
		"/v1/responses": "https://api.openai.com",
	}, cfg.Upstream.PathBaseURLs)
}

func TestLoadUpstreamInvalidBaseURLFailsBoot(t *testing.T) {
	t.Setenv("GUARDRAILS_UPSTREAM_BASE_URL", "not-a-url")

	_, err := config.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GUARDRAILS_UPSTREAM_BASE_URL")
}

func TestLoadUpstreamNonHTTPBaseURLFailsBoot(t *testing.T) {
	t.Setenv("GUARDRAILS_UPSTREAM_BASE_URL", "ftp://example.com")

	_, err := config.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GUARDRAILS_UPSTREAM_BASE_URL")
}

func TestLoadUpstreamInvalidPathOverrideFailsBoot(t *testing.T) {
	t.Setenv("GUARDRAILS_UPSTREAM_PATH_BASE_URLS", "/v1/messages=nonsense")

	_, err := config.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GUARDRAILS_UPSTREAM_PATH_BASE_URLS")
}

func TestEngineAndDataPlaneDefaults(t *testing.T) {
	cfg, err := config.Load()
	require.NoError(t, err)

	assert.True(t, cfg.DataPlaneEnabled, "the data plane stays on unless switched off")
	assert.Empty(t, cfg.Engine.Addr, "the engine API is opt-in")
	assert.False(t, cfg.API.IgnoreUnknownFields, "strict field parsing is the default")
}

func TestEngineAddrWithToken(t *testing.T) {
	t.Setenv("GUARDRAILS_ENGINE_API_ADDR", ":9100")
	t.Setenv("GUARDRAILS_ENGINE_API_TOKEN", "tok")

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, ":9100", cfg.Engine.Addr)
	assert.Equal(t, "tok", cfg.Engine.Token)
}

func TestEngineAddrWithoutTokenFails(t *testing.T) {
	// /v1/unmask hands back original sensitive values, so an engine listener
	// without a token must not start at all.
	t.Setenv("GUARDRAILS_ENGINE_API_ADDR", ":9100")

	_, err := config.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GUARDRAILS_ENGINE_API_TOKEN")
}

func TestDataPlaneDisabledNeedsAnotherSurface(t *testing.T) {
	t.Setenv("GUARDRAILS_DATA_PLANE_ENABLED", "false")

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.False(t, cfg.DataPlaneEnabled)
	// The data plane can be off while the engine API is on: that is exactly the
	// component deployment this switch exists for, and it needs no upstream.
	t.Setenv("GUARDRAILS_ENGINE_API_ADDR", ":9100")
	t.Setenv("GUARDRAILS_ENGINE_API_TOKEN", "tok")
	cfg, err = config.Load()
	require.NoError(t, err)
	assert.Empty(t, cfg.Upstream.BaseURL)
}

func TestAPIIgnoreUnknownFields(t *testing.T) {
	t.Setenv("GUARDRAILS_API_IGNORE_UNKNOWN_FIELDS", "true")

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.True(t, cfg.API.IgnoreUnknownFields)
}
