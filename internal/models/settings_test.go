package models_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
)

func TestParseGuardrailsMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    models.GuardrailsMode
		wantErr bool
	}{
		{"enforce", models.ModeEnforce, false},
		{"detect", models.ModeDetect, false},
		{"", models.ModeEnforce, false}, // pre-mode settings fail toward protection
		{"  Detect ", models.ModeDetect, false},
		{"ENFORCE", models.ModeEnforce, false},
		{"shadow", "", true},
		{"off", "", true},
	}

	for _, tt := range tests {
		t.Run("in="+tt.in, func(t *testing.T) {
			t.Parallel()
			got, err := models.ParseGuardrailsMode(tt.in)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGuardrailsModeIsValid(t *testing.T) {
	t.Parallel()
	assert.True(t, models.ModeEnforce.IsValid())
	assert.True(t, models.ModeDetect.IsValid())
	assert.False(t, models.GuardrailsMode("").IsValid())
	assert.False(t, models.GuardrailsMode("shadow").IsValid())
}
