package demask_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/guardrails/demask"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/scanners/placeholder"
)

func TestDemasker_DemaskChunk_Flush(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		text      string
		setupScan func(scanner *MockPlaceholderScanner, ruleIDs []string)
		want      string
	}{
		{
			name: "exact placeholders are replaced and unknown placeholders remain",
			text: "hello <NAME_1>, keep <UNKNOWN_1>",
			setupScan: func(scanner *MockPlaceholderScanner, ruleIDs []string) {
				scanner.EXPECT().Scan("hello Alice, keep <UNKNOWN_1>", ruleIDs).Return(nil, nil)
			},
			want: "hello Alice, keep <UNKNOWN_1>",
		},
		{
			name: "placeholder scanner demasks non canonical placeholder form",
			text: "hello < name-001 >",
			setupScan: func(scanner *MockPlaceholderScanner, ruleIDs []string) {
				scanner.EXPECT().Scan("hello < name-001 >", ruleIDs).Return([]placeholder.Match{
					{
						RuleID:      "pii.name",
						Start:       len("hello "),
						End:         len("hello < name-001 >"),
						Placeholder: "<NAME_1>",
					},
				}, nil)
			},
			want: "hello Alice",
		},
		{
			name: "scanner matches unknown placeholder and empty placeholder are skipped",
			text: "hello <NAME_1> <EMAIL_1>",
			setupScan: func(scanner *MockPlaceholderScanner, ruleIDs []string) {
				scanner.EXPECT().Scan("hello Alice <EMAIL_1>", ruleIDs).Return([]placeholder.Match{
					{
						RuleID:      "pii.email",
						Start:       len("hello Alice "),
						End:         len("hello Alice <EMAIL_1>"),
						Placeholder: "<EMAIL_1>",
					},
					{
						RuleID:      "empty.placeholder",
						Start:       0,
						End:         len("hello"),
						Placeholder: "",
					},
				}, nil)
			},
			want: "hello Alice <EMAIL_1>",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			reg := NewMockRegistry(ctrl)
			scanner := NewMockPlaceholderScanner(ctrl)
			ruleIDs := []string{"pii.name"}
			reg.EXPECT().GetMaxPlaceholderLenByRuleIDs("pii.name").Return(16)
			tt.setupScan(scanner, ruleIDs)

			d := demask.NewProvider(reg, scanner).NewFactory(maskingState()).Demasker()
			got, err := d.DemaskChunk(context.Background(), tt.text, true)

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDemasker_DemaskChunk_ScannerReplacementOrdering(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		text    string
		matches []placeholder.Match
		want    string
	}{
		{
			name: "same start keeps longest match",
			text: "token",
			matches: []placeholder.Match{
				{RuleID: "short", Start: 0, End: 3, Placeholder: "<SHORT_1>"},
				{RuleID: "long", Start: 0, End: 5, Placeholder: "<LONG_1>"},
			},
			want: "long",
		},
		{
			name: "later overlap is skipped",
			text: "abcdef",
			matches: []placeholder.Match{
				{RuleID: "left", Start: 0, End: 4, Placeholder: "<LEFT_1>"},
				{RuleID: "right", Start: 2, End: 6, Placeholder: "<RIGHT_1>"},
			},
			want: "leftef",
		},
		{
			name: "adjacent matches are kept",
			text: "abcdef",
			matches: []placeholder.Match{
				{RuleID: "right", Start: 3, End: 6, Placeholder: "<RIGHT_1>"},
				{RuleID: "left", Start: 0, End: 3, Placeholder: "<LEFT_1>"},
			},
			want: "leftright",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			reg := NewMockRegistry(ctrl)
			scanner := NewMockPlaceholderScanner(ctrl)
			ruleIDs := []string{"left", "long", "right", "short"}

			reg.EXPECT().GetMaxPlaceholderLenByRuleIDs("left", "long", "right", "short").Return(32)
			scanner.EXPECT().Scan(tt.text, ruleIDs).Return(tt.matches, nil)

			d := demask.NewProvider(reg, scanner).NewFactory(models.MaskingState{
				TriggeredRuleIDs: ruleIDs,
				Replacements: []models.Replacement{
					{RuleID: "short", Original: "short", Placeholder: "<SHORT_1>"},
					{RuleID: "long", Original: "long", Placeholder: "<LONG_1>"},
					{RuleID: "left", Original: "left", Placeholder: "<LEFT_1>"},
					{RuleID: "right", Original: "right", Placeholder: "<RIGHT_1>"},
				},
			}).Demasker()

			got, err := d.DemaskChunk(context.Background(), tt.text, true)

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDemasker_DemaskChunk_ScannerError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	reg := NewMockRegistry(ctrl)
	scanner := NewMockPlaceholderScanner(ctrl)
	ruleIDs := []string{"pii.name"}
	scanErr := errors.New("scan failed")

	reg.EXPECT().GetMaxPlaceholderLenByRuleIDs("pii.name").Return(16)
	scanner.EXPECT().Scan("hello < name-001 >", ruleIDs).Return(nil, scanErr)

	d := demask.NewProvider(reg, scanner).NewFactory(maskingState()).Demasker()
	got, err := d.DemaskChunk(context.Background(), "hello < name-001 >", true)

	require.Error(t, err)
	assert.ErrorIs(t, err, scanErr)
	// On error the demasker hands back its un-emitted content (placeholders
	// intact) so the caller can emit it as a lossless, fail-open fallback.
	assert.Equal(t, "hello < name-001 >", got)
}

func TestDemasker_DemaskChunk_StreamingPending(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	reg := NewMockRegistry(ctrl)
	scanner := NewMockPlaceholderScanner(ctrl)

	reg.EXPECT().GetMaxPlaceholderLenByRuleIDs("pii.name").Return(64).Times(3)

	d := demask.NewProvider(reg, scanner).NewFactory(maskingState()).Demasker()
	scanner.EXPECT().Scan("hello <NA", []string{"pii.name"}).Return(nil, nil)
	out, err := d.DemaskChunk(context.Background(), "hello <NA", false)
	require.NoError(t, err)
	assert.Empty(t, out)

	scanner.EXPECT().Scan("hello Alice!", []string{"pii.name"}).Return(nil, nil)
	out, err = d.DemaskChunk(context.Background(), "ME_1>!", true)
	require.NoError(t, err)
	assert.Equal(t, "hello Alice!", out)

	first := demask.NewProvider(reg, scanner).NewFactory(maskingState()).Demasker()
	second := demask.NewProvider(reg, scanner).NewFactory(maskingState()).Demasker()
	scanner.EXPECT().Scan("first <NA", []string{"pii.name"}).Return(nil, nil)
	out, err = first.DemaskChunk(context.Background(), "first <NA", false)
	require.NoError(t, err)
	assert.Empty(t, out)

	scanner.EXPECT().Scan("second Alice", []string{"pii.name"}).Return(nil, nil)
	out, err = second.DemaskChunk(context.Background(), "second <NAME_1>", true)
	require.NoError(t, err)
	assert.Equal(t, "second Alice", out)

	scanner.EXPECT().Scan("first Alice", []string{"pii.name"}).Return(nil, nil)
	out, err = first.DemaskChunk(context.Background(), "ME_1>", true)
	require.NoError(t, err)
	assert.Equal(t, "first Alice", out)
}

func TestDemasker_DemaskChunk_KeepsExactPlaceholderTailWhenMaxPendingEqualsPlaceholderLen(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	reg := NewMockRegistry(ctrl)
	scanner := NewMockPlaceholderScanner(ctrl)
	ruleIDs := []string{"pii.name"}

	reg.EXPECT().GetMaxPlaceholderLenByRuleIDs("pii.name").Return(len("<NAME_1>"))

	d := demask.NewProvider(reg, scanner).NewFactory(maskingState()).Demasker()

	scanner.EXPECT().Scan("before <NA", ruleIDs).Return(nil, nil)
	out, err := d.DemaskChunk(context.Background(), "before <NA", false)
	require.NoError(t, err)
	assert.Equal(t, "be", out)

	scanner.EXPECT().Scan("fore Alice after", ruleIDs).Return(nil, nil)
	out, err = d.DemaskChunk(context.Background(), "ME_1> after", true)
	require.NoError(t, err)
	assert.Equal(t, "fore Alice after", out)
}

func TestDemasker_DemaskChunk_DoesNotSplitUTF8Rune(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	reg := NewMockRegistry(ctrl)
	scanner := NewMockPlaceholderScanner(ctrl)

	reg.EXPECT().GetMaxPlaceholderLenByRuleIDs().Return(1)
	d := demask.NewProvider(reg, scanner).NewFactory(models.MaskingState{
		Replacements: []models.Replacement{
			{Original: "x", Placeholder: "x"},
		},
	}).Demasker()

	out, err := d.DemaskChunk(context.Background(), "жa", false)
	require.NoError(t, err)
	assert.Equal(t, "ж", out)

	out, err = d.DemaskChunk(context.Background(), "", true)
	require.NoError(t, err)
	assert.Equal(t, "a", out)
}

func maskingState() models.MaskingState {
	return models.MaskingState{
		TriggeredRuleIDs: []string{"pii.name"},
		Replacements: []models.Replacement{
			{
				RuleID:      "pii.name",
				Original:    "Alice",
				Placeholder: "<NAME_1>",
			},
		},
	}
}
