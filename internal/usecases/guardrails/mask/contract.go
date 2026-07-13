package mask

import (
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/registry"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/scanners/sensitive"
)

// Command is the input for the mask use case.
type Command struct {
	DataTypes []models.DataType
	Texts     []string
}

// CommandResponse is the output of the mask use case.
type CommandResponse struct {
	MaskedTexts  []string
	MaskingState models.MaskingState
}

//go:generate mockgen -source=contract.go -destination=mock_test.go -package=mask_test

// SensitiveScanner finds sensitive values for request masking using a
// pre-resolved compiled-rule set (resolved once per request, reused per text).
type SensitiveScanner interface {
	ScanRules(text string, rules []registry.CompiledRule) ([]sensitive.Match, error)
}

// Registry resolves rule IDs and their compiled rules for the masking flow.
// ResolveForDataTypes pins a single registry snapshot so the two never straddle
// a concurrent reload.
type Registry interface {
	ResolveForDataTypes(dataTypes []uint32) ([]string, []registry.CompiledRule)
}
