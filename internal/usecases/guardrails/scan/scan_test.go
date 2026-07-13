package scan_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	maskuc "github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/guardrails/mask"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/guardrails/scan"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/registry"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/scanners/sensitive"
)

func emailRule() rule.Rule {
	return rule.Rule{
		ID:       "builtin_email",
		Name:     "Email",
		DataType: int(models.DataTypePERSONALDATA),
		Regex:    `[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}`,
		Masking:  rule.MaskingConfig{Placeholder: "EMAIL"},
	}
}

func newUseCase(t *testing.T) *scan.UseCase {
	t.Helper()
	builtin := []rule.Rule{emailRule()}
	reg, err := registry.Build(builtin...)
	require.NoError(t, err)
	prod := maskuc.New(maskuc.Deps{Registry: reg, Scanner: sensitive.New(reg)})
	return scan.New(scan.Deps{
		Production:       prod,
		FileRules:        builtin,
		DefaultDataTypes: []models.DataType{models.DataTypePERSONALDATA, models.DataTypeCUSTOM},
	})
}

func TestScanMasksWithBuiltinRule(t *testing.T) {
	t.Parallel()
	uc := newUseCase(t)

	resp, err := uc.Handle(context.Background(), scan.Command{Texts: []string{"reach me at a@b.com"}})
	require.NoError(t, err)

	assert.Equal(t, []string{"builtin_email"}, resp.TriggeredRuleIDs)
	require.Len(t, resp.MaskedTexts, 1)
	assert.Contains(t, resp.MaskedTexts[0], "<EMAIL_1>")
	assert.NotContains(t, resp.MaskedTexts[0], "a@b.com")
	require.Len(t, resp.Replacements, 1)
	assert.Equal(t, "a@b.com", resp.Replacements[0].Original)
}

func TestScanNoMatchEchoesInput(t *testing.T) {
	t.Parallel()
	uc := newUseCase(t)

	resp, err := uc.Handle(context.Background(), scan.Command{Texts: []string{"nothing sensitive here"}})
	require.NoError(t, err)
	assert.Empty(t, resp.TriggeredRuleIDs)
	assert.Equal(t, []string{"nothing sensitive here"}, resp.MaskedTexts)
}

func TestScanCandidateRuleMatches(t *testing.T) {
	t.Parallel()
	uc := newUseCase(t)

	candidate := rule.Rule{
		ID:       "acme_token",
		Name:     "ACME token",
		DataType: int(models.DataTypeCUSTOM),
		Regex:    `\bacme-[0-9a-f]{8}\b`,
		Masking:  rule.MaskingConfig{Placeholder: "ACME_TOKEN"},
	}
	// Narrow the scan to CUSTOM to prove the candidate's own data type is
	// re-added automatically rather than relying on the default scope.
	resp, err := uc.Handle(context.Background(), scan.Command{
		Texts:         []string{"token acme-deadbeef here"},
		DataTypes:     []models.DataType{models.DataTypeCUSTOM},
		CandidateRule: &candidate,
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"acme_token"}, resp.TriggeredRuleIDs)
	assert.Contains(t, resp.MaskedTexts[0], "<ACME_TOKEN_1>")
}

func TestScanInvalidCandidateRule(t *testing.T) {
	t.Parallel()
	uc := newUseCase(t)

	bad := emailRule()
	bad.ID = "bad_rule"
	bad.Regex = "(unclosed"
	_, err := uc.Handle(context.Background(), scan.Command{
		Texts:         []string{"x"},
		CandidateRule: &bad,
	})
	require.ErrorIs(t, err, scan.ErrInvalidRule)
}
