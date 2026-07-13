// Package scan runs a dry-run guardrails scan outside the data path: it reuses
// the production compile + scan + mask pipeline to report what a sample text
// would trigger, without persisting MaskingState or calling any upstream. It
// backs POST /v1/scan (the console "Rule Tester").
package scan

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	maskuc "github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/guardrails/mask"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/registry"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/scanners/sensitive"
)

// ErrInvalidRule marks a candidate rule that failed to compile. The API maps it
// to 400 (the caller sent a bad rule), distinct from an internal scan failure.
var ErrInvalidRule = errors.New("invalid candidate rule")

// Command is the input for a dry-run scan.
type Command struct {
	// Texts are the sample inputs to scan (in order).
	Texts []string
	// DataTypes limits the scan to these data types; empty means "all known".
	DataTypes []models.DataType
	// CandidateRule, when set, is a not-yet-saved rule compiled into an
	// ephemeral registry (built-in rules ∪ candidate) so authors can test it
	// before saving. Its data type is always included in the scan scope.
	CandidateRule *rule.Rule
}

// CommandResponse is the dry-run scan result. Nothing here is persisted.
type CommandResponse struct {
	MaskedTexts        []string
	TriggeredRuleIDs   []string
	TriggeredDataTypes []models.DataType
	// Replacements map each detected original value to its placeholder. Safe to
	// return here — this is the caller's own sample, never stored.
	Replacements  []models.Replacement
	ScanDuration  time.Duration
	TotalDuration time.Duration
}

// Deps are the dependencies of the scan use case.
type Deps struct {
	// Production is the live mask use case (bound to the running registry),
	// used for scans without a candidate rule so results match production.
	Production *maskuc.UseCase
	// FileRules are the built-in rules used to seed the ephemeral registry for
	// candidate-rule scans.
	FileRules []rule.Rule
	// DefaultDataTypes is the scan scope when a Command omits DataTypes.
	DefaultDataTypes []models.DataType
	// KeywordPrefilter mirrors the production scanner setting for candidate scans.
	KeywordPrefilter bool
	// ParallelMinBytes mirrors the production mask setting for candidate scans.
	ParallelMinBytes int
}

// UseCase runs dry-run scans.
type UseCase struct {
	production       *maskuc.UseCase
	fileRules        []rule.Rule
	defaultDataTypes []models.DataType
	keywordPrefilter bool
	parallelMinBytes int
}

// New creates a scan use case.
func New(d Deps) *UseCase {
	return &UseCase{
		production:       d.Production,
		fileRules:        d.FileRules,
		defaultDataTypes: d.DefaultDataTypes,
		keywordPrefilter: d.KeywordPrefilter,
		parallelMinBytes: d.ParallelMinBytes,
	}
}

// Handle runs the scan and maps the mask result to a dry-run response.
func (uc *UseCase) Handle(ctx context.Context, cmd Command) (CommandResponse, error) {
	dataTypes := cmd.DataTypes
	if len(dataTypes) == 0 {
		dataTypes = uc.defaultDataTypes
	}

	masker := uc.production
	if cmd.CandidateRule != nil {
		// Seed a throwaway registry with the built-in rules plus the candidate,
		// compiled through the same error-returning path the production API uses.
		all := make([]rule.Rule, 0, len(uc.fileRules)+1)
		all = append(all, uc.fileRules...)
		all = append(all, *cmd.CandidateRule)
		reg, err := registry.Build(all...)
		if err != nil {
			return CommandResponse{}, fmt.Errorf("%w: %v", ErrInvalidRule, err)
		}
		scanner := sensitive.New(reg, sensitive.WithKeywordPrefilter(uc.keywordPrefilter))
		masker = maskuc.New(
			maskuc.Deps{Registry: reg, Scanner: scanner},
			maskuc.WithParallelMinBytes(uc.parallelMinBytes),
		)
		dataTypes = withDataType(dataTypes, models.DataType(cmd.CandidateRule.DataType))
	}

	started := time.Now()
	resp, err := masker.Handle(ctx, maskuc.Command{DataTypes: dataTypes, Texts: cmd.Texts})
	total := time.Since(started)
	if err != nil {
		return CommandResponse{}, err
	}

	// Handle returns no MaskedTexts when nothing triggered; echo the input
	// unchanged so the caller always gets one masked text per input.
	masked := resp.MaskedTexts
	if masked == nil {
		masked = cmd.Texts
	}

	return CommandResponse{
		MaskedTexts:        masked,
		TriggeredRuleIDs:   resp.MaskingState.TriggeredRuleIDs,
		TriggeredDataTypes: resp.MaskingState.TriggeredDataTypes,
		Replacements:       resp.MaskingState.Replacements,
		TotalDuration:      total,
	}, nil
}

// withDataType appends dt to dts if not already present, so a candidate rule's
// own data type is always in scope even when the caller passed a narrower set.
func withDataType(dts []models.DataType, dt models.DataType) []models.DataType {
	if slices.Contains(dts, dt) {
		return dts
	}
	out := make([]models.DataType, len(dts), len(dts)+1)
	copy(out, dts)
	return append(out, dt)
}
