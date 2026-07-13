package api

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/logging"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/metrics"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/guardrails/scan"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/create"
	deleterule "github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/delete"
	ruleerrors "github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/errors"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/get"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/list"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/setenabled"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/update"

	commonv1 "github.com/cloud-ru-tech/guardrails-llm-filter/pkg/api/proto/cloudru/guardrails/v1/service/common"
	contractsv1 "github.com/cloud-ru-tech/guardrails-llm-filter/pkg/api/proto/cloudru/guardrails/v1/service/contracts"
)

// ListRules returns built-in and/or custom rules filtered by source.
func (c *Controller) ListRules(ctx context.Context, req *contractsv1.ListRulesRequest) (*contractsv1.ListRulesResponse, error) {
	source := req.GetSource()
	if source == "" {
		source = "all"
	}
	var cmd list.Command
	switch source {
	case "all":
		cmd.IncludeBuiltin, cmd.IncludeCustom = true, true
	case sourceBuiltin:
		cmd.IncludeBuiltin = true
	case sourceCustom:
		cmd.IncludeCustom = true
	default:
		return nil, status.Error(codes.InvalidArgument, `source must be "all", "builtin" or "custom"`)
	}

	resp, err := c.list.Handle(ctx, cmd)
	if err != nil {
		return nil, internalError(ctx, "failed to list rules", err)
	}
	out := make([]*commonv1.GuardrailRule, 0, len(resp.Rules))
	for _, v := range resp.Rules {
		out = append(out, ruleToProto(v.Rule, v.Builtin, v.Enabled))
	}
	return &contractsv1.ListRulesResponse{Rules: out}, nil
}

// CreateRule creates a custom rule.
func (c *Controller) CreateRule(ctx context.Context, req *commonv1.GuardrailRule) (*commonv1.GuardrailRule, error) {
	resp, err := c.create.Handle(ctx, create.Command{Rule: protoToRule(req)})
	if err != nil {
		return nil, toRuleMutationError(ctx, err)
	}
	return ruleToProto(resp.Rule, false, resp.Enabled), nil
}

// GetRule reads one rule by ID.
func (c *Controller) GetRule(ctx context.Context, req *contractsv1.GetRuleRequest) (*commonv1.GuardrailRule, error) {
	resp, err := c.get.Handle(ctx, get.Command{ID: req.GetRuleId()})
	switch {
	case errors.Is(err, ruleerrors.ErrNotFound):
		return nil, status.Error(codes.NotFound, err.Error())
	case err != nil:
		return nil, internalError(ctx, "failed to get rule", err)
	default:
		return ruleToProto(resp.Rule, resp.Builtin, resp.Enabled), nil
	}
}

// UpdateRule replaces an existing custom rule. The rule_id comes from the path.
func (c *Controller) UpdateRule(ctx context.Context, req *commonv1.GuardrailRule) (*commonv1.GuardrailRule, error) {
	resp, err := c.update.Handle(ctx, update.Command{Rule: protoToRule(req)})
	if err != nil {
		return nil, toRuleMutationError(ctx, err)
	}
	return ruleToProto(resp.Rule, false, resp.Enabled), nil
}

// SetRuleEnabled enables or disables one rule.
func (c *Controller) SetRuleEnabled(ctx context.Context, req *contractsv1.SetRuleEnabledRequest) (*commonv1.GuardrailRule, error) {
	resp, err := c.setEnabled.Handle(ctx, setenabled.Command{ID: req.GetRuleId(), Enabled: req.GetEnabled()})
	if err != nil {
		return nil, toRuleMutationError(ctx, err)
	}
	return ruleToProto(resp.Rule, resp.Builtin, resp.Enabled), nil
}

// BulkSetRulesEnabled enables or disables many rules. It is best-effort: each id
// is attempted independently and failures are reported per item.
func (c *Controller) BulkSetRulesEnabled(ctx context.Context, req *contractsv1.BulkSetRulesEnabledRequest) (*contractsv1.BulkSetRulesEnabledResponse, error) {
	enabled := req.GetEnabled()
	results := make([]*contractsv1.BulkSetRuleResult, 0, len(req.GetIds()))
	for _, id := range req.GetIds() {
		item := &contractsv1.BulkSetRuleResult{RuleId: id, Status: "ok"}
		if _, err := c.setEnabled.Handle(ctx, setenabled.Command{ID: id, Enabled: enabled}); err != nil {
			item.Status = "error"
			item.Error = err.Error()
		}
		results = append(results, item)
	}
	return &contractsv1.BulkSetRulesEnabledResponse{Results: results}, nil
}

// DeleteRule removes a custom rule by ID.
func (c *Controller) DeleteRule(ctx context.Context, req *contractsv1.DeleteRuleRequest) (*emptypb.Empty, error) {
	if _, err := c.delete.Handle(ctx, deleterule.Command{ID: req.GetRuleId()}); err != nil {
		return nil, toRuleMutationError(ctx, err)
	}
	return &emptypb.Empty{}, nil
}

// GetSettings reads the global guardrails settings.
func (c *Controller) GetSettings(_ context.Context, _ *contractsv1.GetSettingsRequest) (*commonv1.GuardrailsSettings, error) {
	return settingsToProto(c.settings.Global()), nil
}

// UpdateSettings replaces the global guardrails settings.
func (c *Controller) UpdateSettings(ctx context.Context, req *commonv1.GuardrailsSettings) (*commonv1.GuardrailsSettings, error) {
	gs, err := protoToSettings(req)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := c.settings.Update(ctx, gs); err != nil {
		return nil, internalError(ctx, "failed to save settings", err)
	}
	return settingsToProto(gs), nil
}

// GetDataTypes lists the toggleable data-type groups plus CUSTOM.
func (c *Controller) GetDataTypes(_ context.Context, _ *contractsv1.GetDataTypesRequest) (*contractsv1.GetDataTypesResponse, error) {
	// The data-type groups come from two rule files that may declare the same
	// numeric id (with different descriptions); dedupe by id so each category
	// appears once, keeping the first (primary rule file) occurrence.
	out := make([]*contractsv1.DataTypeInfo, 0, len(c.dataTypes)+1)
	seen := make(map[int]bool, len(c.dataTypes)+1)
	for _, dt := range c.dataTypes {
		if seen[dt.DataType] {
			continue
		}
		seen[dt.DataType] = true
		out = append(out, &contractsv1.DataTypeInfo{
			DataType:    int32(dt.DataType), //nolint:gosec // data-type IDs are small
			Name:        dt.Name,
			DisplayName: dt.DisplayName,
			Description: dt.Description,
		})
	}
	if !seen[int(models.DataTypeCUSTOM)] {
		out = append(out, &contractsv1.DataTypeInfo{
			DataType:    int32(models.DataTypeCUSTOM),
			Name:        models.DataTypeCUSTOM.String(),
			DisplayName: "Custom",
			Description: "Rules created via the configuration API without a built-in category.",
		})
	}
	return &contractsv1.GetDataTypesResponse{DataTypes: out}, nil
}

// Scan runs a dry-run masking check on sample text(s).
func (c *Controller) Scan(ctx context.Context, req *contractsv1.ScanRequest) (*contractsv1.ScanResponse, error) {
	texts := req.GetTexts()
	if len(texts) == 0 && req.GetText() != "" {
		texts = []string{req.GetText()}
	}
	if len(texts) == 0 {
		return nil, status.Error(codes.InvalidArgument, `"text" or "texts" is required`)
	}

	cmd := scan.Command{Texts: texts}
	for _, d := range req.GetDataTypes() {
		cmd.DataTypes = append(cmd.DataTypes, models.DataType(d)) //nolint:gosec // enum values are non-negative
	}
	if req.GetCandidateRule() != nil {
		rl := protoToRule(req.GetCandidateRule())
		cmd.CandidateRule = &rl
	}

	resp, err := c.scan.Handle(ctx, cmd)
	switch {
	case errors.Is(err, scan.ErrInvalidRule):
		return nil, status.Error(codes.InvalidArgument, err.Error())
	case err != nil:
		return nil, internalError(ctx, "scan failed", err)
	default:
		return scanToProto(resp), nil
	}
}

// ListAuditRecords lists masking audit records.
func (c *Controller) ListAuditRecords(ctx context.Context, req *contractsv1.ListAuditRecordsRequest) (*contractsv1.ListAuditRecordsResponse, error) {
	if c.audit == nil {
		return nil, status.Error(codes.FailedPrecondition, "audit trail is disabled")
	}
	q := repository.AuditQuery{
		Model:    req.GetModel(),
		Path:     req.GetPath(),
		RuleID:   req.GetRuleId(),
		DataType: models.DataType(req.GetDataType()), //nolint:gosec // enum values are non-negative
		Limit:    int(req.GetLimit()),
		Cursor:   req.GetCursor(),
	}
	if req.GetSince() != nil {
		q.Since = req.GetSince().AsTime()
	}
	if req.GetUntil() != nil {
		q.Until = req.GetUntil().AsTime()
	}

	page, err := c.audit.ListAuditRecords(ctx, q)
	switch {
	case errors.Is(err, repository.ErrBadCursor):
		return nil, status.Error(codes.InvalidArgument, "cursor must come from a previous response")
	case err != nil:
		metrics.IncAuditStoreFailure("list")
		return nil, internalError(ctx, "failed to list audit records", err)
	}

	records := make([]*commonv1.AuditRecord, 0, len(page.Records))
	for i := range page.Records {
		c.decryptAuditOriginals(ctx, &page.Records[i])
		records = append(records, auditRecordToProto(page.Records[i]))
	}
	return &contractsv1.ListAuditRecordsResponse{Records: records, NextCursor: page.NextCursor}, nil
}

// decryptAuditOriginals opens any encrypted AuditReplacement.Original values in
// place before the record leaves the API. A value that cannot be decrypted
// (wrong key, or encryption now disabled) is dropped to placeholder-only, so a
// misconfiguration never leaks ciphertext and never fails the read. Plaintext
// values (StoreOriginalTexts=plain) pass through unchanged.
func (c *Controller) decryptAuditOriginals(ctx context.Context, rec *models.AuditRecord) {
	if c.originals == nil {
		return
	}
	for i := range rec.Replacements {
		orig := rec.Replacements[i].Original
		if orig == "" {
			continue
		}
		plain, err := c.originals.DecryptString(orig)
		if err != nil {
			logging.Debug(ctx, "Dropping undecryptable audit original",
				"request_id", rec.RequestID, "error", err)
			rec.Replacements[i].Original = ""
			continue
		}
		rec.Replacements[i].Original = plain
	}
}

// GetAuditRecord reads one masking audit record by request ID.
func (c *Controller) GetAuditRecord(ctx context.Context, req *contractsv1.GetAuditRecordRequest) (*commonv1.AuditRecord, error) {
	if c.audit == nil {
		return nil, status.Error(codes.FailedPrecondition, "audit trail is disabled")
	}
	rec, err := c.audit.GetAuditRecord(ctx, req.GetRequestId())
	switch {
	case errors.Is(err, repository.ErrNotFound):
		return nil, status.Error(codes.NotFound, "audit record not found")
	case err != nil:
		metrics.IncAuditStoreFailure("get")
		return nil, internalError(ctx, "failed to get audit record", err)
	default:
		c.decryptAuditOriginals(ctx, &rec)
		return auditRecordToProto(rec), nil
	}
}

// GetVersion reports the service build identity and live masking mode.
func (c *Controller) GetVersion(_ context.Context, _ *contractsv1.GetVersionRequest) (*contractsv1.GetVersionResponse, error) {
	return &contractsv1.GetVersionResponse{
		Version:      c.buildInfo.Version,
		Commit:       c.buildInfo.Commit,
		Date:         c.buildInfo.Date,
		Mode:         string(c.settings.Global().Mode),
		StoreBackend: c.buildInfo.StoreBackend,
		Topology:     c.buildInfo.Topology,
	}, nil
}
