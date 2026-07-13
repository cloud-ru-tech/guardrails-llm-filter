// Package validation holds the shared rule-content validation used by the
// create and update use cases. It runs the exact production compile path
// (registry.CompileRule) plus the static API constraints (ID alphabet, known
// data type), so the API rejects the same rules the data path would.
package validation

import (
	"fmt"
	"regexp"

	ruleerrors "github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/errors"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/registry"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
)

// ruleIDRe bounds API-supplied rule IDs to a safe, predictable alphabet.
var ruleIDRe = regexp.MustCompile(`^[a-z0-9_.-]{1,128}$`)

// Limits bounds custom-rule content to protect the request hot path, where
// every rule is evaluated against every request.
type Limits struct {
	// MaxPatternLen bounds a rule's regex length. 0 disables the check.
	MaxPatternLen int
}

// Validate checks a rule's ID alphabet, data type, regex length and that it
// compiles via the real production path. It returns a
// *ruleerrors.ValidationError on any content problem, or nil when the rule is
// acceptable.
func Validate(r rule.Rule, limits Limits) error {
	if !ruleIDRe.MatchString(r.ID) {
		return &ruleerrors.ValidationError{Err: fmt.Errorf("rule_id must match %s", ruleIDRe.String())}
	}
	dt := models.DataType(r.DataType) //nolint:gosec // enum range checked below
	if r.DataType < 0 || !dt.IsValid() || dt == models.DataTypeUNSPECIFIED {
		return &ruleerrors.ValidationError{Err: fmt.Errorf("unknown data_type %d", r.DataType)}
	}
	if limits.MaxPatternLen > 0 && len(r.Regex) > limits.MaxPatternLen {
		return &ruleerrors.ValidationError{
			Err: fmt.Errorf("regex too long: %d bytes exceeds limit of %d", len(r.Regex), limits.MaxPatternLen),
		}
	}
	if _, err := registry.CompileRule(registry.NewRegistry(), r); err != nil {
		return &ruleerrors.ValidationError{Err: err}
	}
	return nil
}
