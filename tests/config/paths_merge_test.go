// Package config_test exercises GUARDRAILS_PATHS end-to-end: config.Load builds
// the path map, which the PathResolver then uses to route requests. It proves a
// partial override can never silently disable masking for a core endpoint.
package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/config"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
)

func TestPartialPathsOverrideStillGuardsCoreEndpoints(t *testing.T) {
	// Operator adds one custom proxy mount and (mistakenly) restates nothing else.
	t.Setenv("GUARDRAILS_PATHS", "/openai/v1/chat/completions:chat_completions")

	cfg, err := config.Load()
	require.NoError(t, err)

	resolver, err := models.NewPathResolver(cfg.Guardrails.Paths)
	require.NoError(t, err)

	cases := []struct {
		path string
		want models.APIFormat
	}{
		{"/openai/v1/chat/completions", models.APIFormatChatCompletions}, // the custom mount
		{"/v1/chat/completions", models.APIFormatChatCompletions},        // core, still guarded
		{"/v1/messages", models.APIFormatMessages},
		{"/v1/responses", models.APIFormatResponses},
	}
	for _, tc := range cases {
		got, ok := resolver.Resolve(tc.path)
		assert.True(t, ok, "path %q must resolve (be guarded)", tc.path)
		assert.Equal(t, tc.want, got, "path %q format", tc.path)
	}
}
