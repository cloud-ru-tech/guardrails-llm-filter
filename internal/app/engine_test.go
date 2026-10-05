package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/config"
)

// TestDataPlaneDisabledNeedsNoUpstream covers the component deployment: with
// GUARDRAILS_DATA_PLANE_ENABLED=false there is no gateway listener at all, so
// the missing-upstream panic must not be reachable.
func TestDataPlaneDisabledNeedsNoUpstream(t *testing.T) {
	cfg := &config.Config{}
	cfg.DataPlaneEnabled = false

	a := New(cfg)

	require.Nil(t, a.GatewayServer())
	assert.Empty(t, cfg.Upstream.BaseURL)
}

// TestDataPlaneEnabledRequiresUpstream pins the previous behavior: with the data
// plane on, an upstream is mandatory and its absence fails the boot loudly.
func TestDataPlaneEnabledRequiresUpstream(t *testing.T) {
	cfg := &config.Config{}
	cfg.DataPlaneEnabled = true

	a := New(cfg)

	assert.Panics(t, func() { a.GatewayServer() })
}

// TestEngineServerDisabledWithoutAddr: the engine API is opt-in, and an empty
// address must not build a handler (which would need the rule files loaded).
func TestEngineServerDisabledWithoutAddr(t *testing.T) {
	cfg := &config.Config{}

	a := New(cfg)

	assert.Nil(t, a.EngineHandler())
	assert.Nil(t, a.EngineServer())
}
