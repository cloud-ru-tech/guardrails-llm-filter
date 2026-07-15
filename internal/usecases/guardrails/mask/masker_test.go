package mask

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/scanners/sensitive"
)

func TestMaskerMaskText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		text    string
		matches []sensitive.Match
		want    string
	}{
		{
			name: "no matches returns original text",
			text: "plain text",
			want: "plain text",
		},
		{
			name: "masks byte span after utf8 prefix",
			text: "до secret после",
			matches: []sensitive.Match{
				{
					RuleID:      "credentials.secret",
					DataType:    int(models.DataTypeCREDENTIALS),
					Start:       len("до "),
					End:         len("до secret"),
					FullText:    "secret",
					Placeholder: "SECRET",
				},
			},
			want: "до <SECRET_1> после",
		},
		{
			name: "skips overlapping later match",
			text: "abcdef",
			matches: []sensitive.Match{
				{
					RuleID:      "left",
					DataType:    int(models.DataTypeCREDENTIALS),
					Start:       0,
					End:         4,
					FullText:    "abcd",
					Placeholder: "SECRET",
				},
				{
					RuleID:      "right",
					DataType:    int(models.DataTypeAPIKEYS),
					Start:       2,
					End:         6,
					FullText:    "cdef",
					Placeholder: "KEY",
				},
			},
			want: "<SECRET_1>ef",
		},
		{
			name: "skips empty full text and empty placeholder",
			text: "token=secret",
			matches: []sensitive.Match{
				{
					RuleID:      "empty.full_text",
					DataType:    int(models.DataTypeCREDENTIALS),
					Start:       len("token="),
					End:         len("token=secret"),
					FullText:    "",
					Placeholder: "SECRET",
				},
				{
					RuleID:      "empty.placeholder",
					DataType:    int(models.DataTypeCREDENTIALS),
					Start:       len("token="),
					End:         len("token=secret"),
					FullText:    "secret",
					Placeholder: "",
				},
			},
			want: "token=secret",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newMasker(nil)

			got := m.maskText(tt.text, tt.matches)

			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMaskerStateAcrossCalls(t *testing.T) {
	t.Parallel()

	m := newMasker(nil)

	gotFirst := m.maskText("secret and api", []sensitive.Match{
		maskerMatch("credentials.secret", models.DataTypeCREDENTIALS, "secret and api", "secret", "SECRET"),
		maskerMatch("api.key", models.DataTypeAPIKEYS, "secret and api", "api", "API_KEY"),
	})
	gotSecond := m.maskText("secret and other", []sensitive.Match{
		maskerMatch("credentials.secret.repeat", models.DataTypeCREDENTIALS, "secret and other", "secret", "DIFFERENT"),
		maskerMatch("credentials.other", models.DataTypeCREDENTIALS, "secret and other", "other", "SECRET"),
	})

	assert.Equal(t, "<SECRET_1> and <API_KEY_1>", gotFirst)
	assert.Equal(t, "<SECRET_1> and <SECRET_2>", gotSecond)
	assert.Equal(t, []models.Replacement{
		{
			RuleID:      "credentials.secret",
			Original:    "secret",
			Placeholder: "<SECRET_1>",
		},
		{
			RuleID:      "api.key",
			Original:    "api",
			Placeholder: "<API_KEY_1>",
		},
		{
			RuleID:      "credentials.other",
			Original:    "other",
			Placeholder: "<SECRET_2>",
		},
	}, m.replacements())
	// triggeredRules must be deterministically ordered (sorted), like
	// triggeredDataTypes — the IDs flow into the response header, audit records
	// and persisted MaskingState, which downstream consumers compare.
	assert.Equal(t, []string{
		"api.key",
		"credentials.other",
		"credentials.secret",
		"credentials.secret.repeat",
	}, m.triggeredRules())
	assert.Equal(t, []models.DataType{models.DataTypeCREDENTIALS, models.DataTypeAPIKEYS}, m.triggeredDataTypes())
}

func TestMaskerTriggeredDataTypesFiltersInvalidValues(t *testing.T) {
	t.Parallel()

	m := newMasker(nil)

	got := m.maskText("a b c", []sensitive.Match{
		{
			RuleID:      "unspecified",
			DataType:    int(models.DataTypeUNSPECIFIED),
			Start:       0,
			End:         1,
			FullText:    "a",
			Placeholder: "A",
		},
		{
			RuleID:      "invalid",
			DataType:    999,
			Start:       2,
			End:         3,
			FullText:    "b",
			Placeholder: "B",
		},
		{
			RuleID:      "valid",
			DataType:    int(models.DataTypePERSONALDATA),
			Start:       4,
			End:         5,
			FullText:    "c",
			Placeholder: "C",
		},
	})

	assert.Equal(t, "<A_1> <B_1> <C_1>", got)
	assert.Equal(t, []models.DataType{models.DataTypePERSONALDATA}, m.triggeredDataTypes())
}

func TestMaskerPlaceholderHelpers(t *testing.T) {
	t.Parallel()

	m := newMasker(nil)

	placeholder, created := m.placeholderForOriginal("secret", "SECRET")
	require.True(t, created)
	assert.Equal(t, "<SECRET_1>", placeholder)

	placeholder, created = m.placeholderForOriginal("secret", "DIFFERENT")
	require.False(t, created)
	assert.Equal(t, "<SECRET_1>", placeholder)

	assert.Equal(t, "<SECRET_2>", m.nextPlaceholder("SECRET"))
	assert.Equal(t, "<KEY_1>", m.nextPlaceholder("KEY"))
}

func TestDataTypesToUint32s(t *testing.T) {
	t.Parallel()

	got := dataTypesToUint32s([]models.DataType{
		models.DataTypeCREDENTIALS,
		models.DataTypeAPIKEYS,
		models.DataTypePERSONALDATA,
	})

	assert.Equal(t, []uint32{
		uint32(models.DataTypeCREDENTIALS),
		uint32(models.DataTypeAPIKEYS),
		uint32(models.DataTypePERSONALDATA),
	}, got)
}

func maskerMatch(ruleID string, dataType models.DataType, text, fullText, placeholder string) sensitive.Match {
	start := indexOrPanicMasker(text, fullText)
	return sensitive.Match{
		RuleID:      ruleID,
		DataType:    int(dataType),
		Start:       start,
		End:         start + len(fullText),
		FullText:    fullText,
		Placeholder: placeholder,
	}
}

func indexOrPanicMasker(s, substr string) int {
	if idx := strings.Index(s, substr); idx >= 0 {
		return idx
	}
	panic("substring not found")
}
