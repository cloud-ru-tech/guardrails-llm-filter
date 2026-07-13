package settings

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
)

func TestEffective(t *testing.T) {
	t.Parallel()

	allTypes := []models.DataType{
		models.DataTypeCREDENTIALS,
		models.DataTypeAPIKEYS,
		models.DataTypeACCESSTOKENS,
		models.DataTypeIPADDRESSES,
		models.DataTypePERSONALDATA,
	}
	enabledGlobal := models.GuardrailsSettings{Enabled: true, DataTypes: allTypes}

	tests := []struct {
		name   string
		global models.GuardrailsSettings
		header string
		want   models.EffectiveSettings
	}{
		{
			name:   "no header keeps global",
			global: enabledGlobal,
			header: "",
			want:   models.EffectiveSettings{Enabled: true, DataTypes: allTypes},
		},
		{
			name:   "globally disabled stays disabled regardless of header",
			global: models.GuardrailsSettings{Enabled: false, DataTypes: allTypes},
			header: "1,2,3",
			want:   models.EffectiveSettings{Enabled: false},
		},
		{
			name:   "numeric narrowing",
			global: enabledGlobal,
			header: "1,4",
			want: models.EffectiveSettings{Enabled: true, DataTypes: []models.DataType{
				models.DataTypeCREDENTIALS, models.DataTypeIPADDRESSES,
			}},
		},
		{
			name:   "names case-insensitive",
			global: enabledGlobal,
			header: "Credentials, PERSONAL_DATA",
			want: models.EffectiveSettings{Enabled: true, DataTypes: []models.DataType{
				models.DataTypeCREDENTIALS, models.DataTypePERSONALDATA,
			}},
		},
		{
			name:   "mixed numbers and names",
			global: enabledGlobal,
			header: "2,personal_data",
			want: models.EffectiveSettings{Enabled: true, DataTypes: []models.DataType{
				models.DataTypeAPIKEYS, models.DataTypePERSONALDATA,
			}},
		},
		{
			name:   "none disables the request",
			global: enabledGlobal,
			header: "none",
			want:   models.EffectiveSettings{Enabled: false},
		},
		{
			name:   "header cannot expand beyond global",
			global: models.GuardrailsSettings{Enabled: true, DataTypes: []models.DataType{models.DataTypeCREDENTIALS}},
			header: "1,5",
			want: models.EffectiveSettings{Enabled: true, DataTypes: []models.DataType{
				models.DataTypeCREDENTIALS,
			}},
		},
		{
			name:   "empty intersection disables",
			global: models.GuardrailsSettings{Enabled: true, DataTypes: []models.DataType{models.DataTypeCREDENTIALS}},
			header: "5",
			want:   models.EffectiveSettings{Enabled: false},
		},
		{
			name:   "garbage token ignores whole header (fail toward protection)",
			global: enabledGlobal,
			header: "1,definitely-not-a-type",
			want:   models.EffectiveSettings{Enabled: true, DataTypes: allTypes},
		},
		{
			name:   "unknown numeric ignores whole header",
			global: enabledGlobal,
			header: "42",
			want:   models.EffectiveSettings{Enabled: true, DataTypes: allTypes},
		},
		{
			name:   "zero (UNSPECIFIED) ignores whole header",
			global: enabledGlobal,
			header: "0",
			want:   models.EffectiveSettings{Enabled: true, DataTypes: allTypes},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Effective(tt.global, tt.header)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestEffectivePreservesMode(t *testing.T) {
	t.Parallel()

	global := models.GuardrailsSettings{
		Enabled:   true,
		DataTypes: []models.DataType{models.DataTypeCREDENTIALS, models.DataTypePERSONALDATA},
		Mode:      models.ModeDetect,
	}

	// Mode is global-only: the header narrows data types but never flips it.
	for _, header := range []string{"", "credentials", "none", "garbage-token"} {
		got := Effective(global, header)
		assert.Equal(t, models.ModeDetect, got.Mode, "header %q", header)
	}

	// Globally disabled still reports the configured mode.
	global.Enabled = false
	assert.Equal(t, models.ModeDetect, Effective(global, "").Mode)
}

func TestParseDataTypes(t *testing.T) {
	t.Parallel()

	t.Run("deduplicates", func(t *testing.T) {
		t.Parallel()
		got, err := ParseDataTypes("1,credentials,1")
		require.NoError(t, err)
		assert.Equal(t, []models.DataType{models.DataTypeCREDENTIALS}, got)
	})

	t.Run("custom type parses", func(t *testing.T) {
		t.Parallel()
		got, err := ParseDataTypes("custom")
		require.NoError(t, err)
		assert.Equal(t, []models.DataType{models.DataTypeCUSTOM}, got)
	})

	t.Run("empty errors", func(t *testing.T) {
		t.Parallel()
		_, err := ParseDataTypes(" , ")
		require.Error(t, err)
	})
}
