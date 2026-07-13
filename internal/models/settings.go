package models

import (
	"fmt"
	"strings"
)

// GuardrailsMode selects what the service does when sensitive data is found.
type GuardrailsMode string

const (
	// ModeEnforce masks sensitive values in request bodies and demasks
	// responses. The default.
	ModeEnforce GuardrailsMode = "enforce"
	// ModeDetect (shadow mode) scans and records metrics/audit but never
	// mutates traffic. Used to evaluate what would be masked before
	// enabling enforcement.
	ModeDetect GuardrailsMode = "detect"
)

// ParseGuardrailsMode parses a mode value case-insensitively. The empty
// string resolves to ModeEnforce: settings persisted before the mode field
// existed unmarshal to "", and missing configuration must fail toward the
// more protective behavior.
func ParseGuardrailsMode(s string) (GuardrailsMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", string(ModeEnforce):
		return ModeEnforce, nil
	case string(ModeDetect):
		return ModeDetect, nil
	default:
		return "", fmt.Errorf("unknown guardrails mode %q (valid: enforce, detect)", s)
	}
}

// IsValid reports whether m is a known mode value.
func (m GuardrailsMode) IsValid() bool {
	return m == ModeEnforce || m == ModeDetect
}

// GuardrailsSettings is the global (instance-wide) guardrails configuration.
// It replaces the per-(project, model) settings of the managed deployment:
// the OSS service applies one policy to all traffic, optionally narrowed
// per request via a trusted gateway header.
type GuardrailsSettings struct {
	Enabled   bool           `json:"enabled"`
	DataTypes []DataType     `json:"data_types"`
	Mode      GuardrailsMode `json:"mode,omitempty"`
}

// EffectiveSettings is the per-request resolution of the global settings
// combined with the optional narrow-only header override.
type EffectiveSettings struct {
	Enabled   bool
	DataTypes []DataType
	Mode      GuardrailsMode
}
