package demask

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
)

func TestBuildLookupIndex(t *testing.T) {
	t.Parallel()

	cfg := &DemaskConfig{}
	cfg.buildLookupIndex([]models.Replacement{
		{Original: "ignored", Placeholder: ""},
		{Original: "short", Placeholder: "<PLACEHOLDER_1>"},
		{Original: "long", Placeholder: "<PLACEHOLDER_10>"},
		{Original: "duplicate ignored", Placeholder: "<PLACEHOLDER_1>"},
	})

	assert.Equal(t, map[string]string{
		"<PLACEHOLDER_1>":  "short",
		"<PLACEHOLDER_10>": "long",
	}, cfg.placeholderToOriginal)
	assert.Equal(t, "short long", cfg.exactReplacer.Replace("<PLACEHOLDER_1> <PLACEHOLDER_10>"))
	assert.Equal(t, len("<PLACEHOLDER_10>"), cfg.maxPending)
}

func TestBuildLookupIndexJSONEscaped(t *testing.T) {
	t.Parallel()

	cfg := &DemaskConfig{}
	cfg.buildLookupIndex([]models.Replacement{
		{Original: `say "hi"`, Placeholder: "<SECRET_1>"},
		{Original: `C:\tmp\key`, Placeholder: "<SECRET_2>"},
		{Original: "plain <tag> & more", Placeholder: "<SECRET_3>"},
	})

	assert.Equal(t, map[string]string{
		"<SECRET_1>": `say \"hi\"`,
		"<SECRET_2>": `C:\\tmp\\key`,
		"<SECRET_3>": "plain <tag> & more", // no HTML escaping
	}, cfg.placeholderToJSON)
	assert.Equal(t, `say \"hi\"`, cfg.jsonReplacer.Replace("<SECRET_1>"))
}
