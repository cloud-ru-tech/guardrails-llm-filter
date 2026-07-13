package api

import (
	"fmt"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/guardrails/scan"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"

	commonv1 "github.com/cloud-ru-tech/guardrails-llm-filter/pkg/api/proto/cloudru/guardrails/v1/service/common"
	contractsv1 "github.com/cloud-ru-tech/guardrails-llm-filter/pkg/api/proto/cloudru/guardrails/v1/service/contracts"
)

// ruleToProto maps an engine rule and its source/enabled state to the wire
// type. source and enabled are response-only.
func ruleToProto(r rule.Rule, builtin, enabled bool) *commonv1.GuardrailRule {
	source := sourceCustom
	if builtin {
		source = sourceBuiltin
	}
	validators := make([]string, 0, len(r.Validators))
	for _, v := range r.Validators {
		validators = append(validators, string(v))
	}
	capture := make([]int32, 0, len(r.Masking.CaptureGroups))
	for _, g := range r.Masking.CaptureGroups {
		capture = append(capture, int32(g)) //nolint:gosec // capture-group indices are small
	}
	en := enabled
	return &commonv1.GuardrailRule{
		RuleId:     r.ID,
		Name:       r.Name,
		Group:      r.Group,
		DataType:   commonv1.DataType(r.DataType), //nolint:gosec // rule data types are the enum range
		Regex:      r.Regex,
		Keywords:   r.Keywords,
		Validators: validators,
		MinLength:  int32(r.MinLength), //nolint:gosec // rule lengths are bounded by MaxPatternLen
		Entropy:    r.Entropy,
		Banlist:    r.Banlist,
		Masking: &commonv1.Masking{
			CaptureGroups: capture,
			Placeholder:   r.Masking.Placeholder,
		},
		Source:  source,
		Enabled: &en,
	}
}

// protoToRule maps a wire rule to the engine type: DefaultOn is always true;
// source/enabled are ignored on input.
func protoToRule(p *commonv1.GuardrailRule) rule.Rule {
	if p == nil {
		return rule.Rule{}
	}
	validators := make([]rule.ValidatorType, 0, len(p.GetValidators()))
	for _, v := range p.GetValidators() {
		validators = append(validators, rule.ValidatorType(v))
	}
	capture := make([]int, 0, len(p.GetMasking().GetCaptureGroups()))
	for _, g := range p.GetMasking().GetCaptureGroups() {
		capture = append(capture, int(g))
	}
	return rule.Rule{
		ID:         p.GetRuleId(),
		Name:       p.GetName(),
		Group:      p.GetGroup(),
		DataType:   int(p.GetDataType()),
		Regex:      p.GetRegex(),
		Keywords:   p.GetKeywords(),
		Validators: validators,
		MinLength:  int(p.GetMinLength()),
		Entropy:    p.GetEntropy(),
		Banlist:    p.GetBanlist(),
		DefaultOn:  true,
		Masking: rule.MaskingConfig{
			CaptureGroups: capture,
			Placeholder:   p.GetMasking().GetPlaceholder(),
		},
	}
}

// dataTypesToProto maps engine data types to the wire enum.
func dataTypesToProto(dts []models.DataType) []commonv1.DataType {
	out := make([]commonv1.DataType, 0, len(dts))
	for _, dt := range dts {
		out = append(out, commonv1.DataType(dt))
	}
	return out
}

// protoToSettings validates and maps wire settings to the domain type. It
// rejects the UNSPECIFIED data type and deduplicates.
func protoToSettings(p *commonv1.GuardrailsSettings) (models.GuardrailsSettings, error) {
	dataTypes := make([]models.DataType, 0, len(p.GetDataTypes()))
	seen := make(map[models.DataType]bool, len(p.GetDataTypes()))
	for _, d := range p.GetDataTypes() {
		dt := models.DataType(d) //nolint:gosec // enum values are non-negative
		if dt == models.DataTypeUNSPECIFIED || !dt.IsValid() {
			return models.GuardrailsSettings{}, fmt.Errorf("unknown data type %s", dt.String())
		}
		if !seen[dt] {
			seen[dt] = true
			dataTypes = append(dataTypes, dt)
		}
	}
	// mode is case-insensitive; empty resolves to enforce (models.ParseGuardrailsMode).
	mode, err := models.ParseGuardrailsMode(p.GetMode())
	if err != nil {
		return models.GuardrailsSettings{}, err
	}
	return models.GuardrailsSettings{
		Enabled:   p.GetEnabled(),
		DataTypes: dataTypes,
		Mode:      mode,
	}, nil
}

// settingsToProto maps global settings to the wire type. Mode is emitted as the
// string value ("enforce"/"detect").
func settingsToProto(gs models.GuardrailsSettings) *commonv1.GuardrailsSettings {
	return &commonv1.GuardrailsSettings{
		Enabled:   gs.Enabled,
		DataTypes: dataTypesToProto(gs.DataTypes),
		Mode:      string(gs.Mode),
	}
}

func scanToProto(resp scan.CommandResponse) *contractsv1.ScanResponse {
	placeholders := make([]*commonv1.Placeholder, 0, len(resp.Replacements))
	for _, rep := range resp.Replacements {
		placeholders = append(placeholders, &commonv1.Placeholder{
			Placeholder: rep.Placeholder,
			Original:    rep.Original,
			RuleId:      rep.RuleID,
		})
	}
	return &contractsv1.ScanResponse{
		MaskedTexts:        resp.MaskedTexts,
		TriggeredRuleIds:   resp.TriggeredRuleIDs,
		TriggeredDataTypes: dataTypesToProto(resp.TriggeredDataTypes),
		Placeholders:       placeholders,
		TotalMs:            int32(resp.TotalDuration.Milliseconds()), //nolint:gosec // scan durations fit int32 ms
	}
}

func auditRecordToProto(rec models.AuditRecord) *commonv1.AuditRecord {
	replacements := make([]*commonv1.AuditReplacement, 0, len(rec.Replacements))
	for _, rep := range rec.Replacements {
		replacements = append(replacements, &commonv1.AuditReplacement{
			RuleId:      rep.RuleID,
			DataType:    commonv1.DataType(rep.DataType),
			Placeholder: rep.Placeholder,
			Original:    rep.Original, // decrypted by the handler; empty unless opted in
		})
	}
	return &commonv1.AuditRecord{
		RequestId:           rec.RequestID,
		Timestamp:           timestamppb.New(rec.Timestamp),
		Mode:                rec.Mode,
		Model:               rec.Model,
		Path:                rec.Path,
		TriggeredRuleIds:    rec.TriggeredRuleIDs,
		TriggeredDataTypes:  dataTypesToProto(rec.TriggeredDataTypes),
		Replacements:        replacements,
		MaskedTexts:         rec.MaskedTexts,
		MaskedResponseTexts: rec.MaskedResponseTexts,
	}
}
