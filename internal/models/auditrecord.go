package models

import "time"

// AuditReplacement is the audit-safe projection of a Replacement. By default
// it carries no original value — audit records must not leak raw sensitive
// values. The Original field stays empty unless the operator explicitly opts
// in via GUARDRAILS_AUDIT_STORE_ORIGINAL_TEXTS (off by default); see Original.
type AuditReplacement struct {
	RuleID      string   `json:"rule_id"`
	DataType    DataType `json:"data_type"` // resolved from the rule at write time; UNSPECIFIED if the rule is gone
	Placeholder string   `json:"placeholder"`
	// Original is the raw pre-masking value behind Placeholder. It is populated
	// ONLY when GUARDRAILS_AUDIT_STORE_ORIGINAL_TEXTS is "plain" or "encrypted"
	// (default "off" leaves it empty and unserialized). In "encrypted" mode the
	// stored value is a statecodec AES-256-GCM envelope, decrypted by the audit
	// read path before it leaves the API. SECURITY: this is raw sensitive data —
	// never log it; keep the store access-controlled.
	Original string `json:"original,omitempty"`
}

// AuditRecord is one per-request masking audit entry. JSON tags define the
// wire format used by external stores and the HTTP API and must stay stable
// across versions.
type AuditRecord struct {
	RequestID          string             `json:"request_id"`
	Timestamp          time.Time          `json:"timestamp"` // UTC, set by the recorder
	Model              string             `json:"model,omitempty"`
	Path               string             `json:"path"`
	TriggeredRuleIDs   []string           `json:"triggered_rule_ids"`
	TriggeredDataTypes []DataType         `json:"triggered_data_types"`
	Replacements       []AuditReplacement `json:"replacements"`
	// Mode records whether masking was applied ("enforce") or only
	// detected ("detect"); empty in records written before the field existed.
	Mode string `json:"mode,omitempty"`
	// MaskedTexts holds the post-masking (placeholder-substituted) request
	// text fields. Populated only when GUARDRAILS_AUDIT_STORE_MASKED_TEXTS
	// is enabled; still user prompt content — see the operator checklist.
	MaskedTexts []string `json:"masked_texts,omitempty"`
	// MaskedResponseTexts holds the post-masking (placeholder-substituted)
	// model response text fields — the response counterpart of MaskedTexts.
	// Populated only when GUARDRAILS_AUDIT_STORE_MASKED_RESPONSE_TEXTS is
	// enabled, and set by a best-effort response-phase update (see
	// audit.Recorder.RecordResponse). Placeholders in place, no originals.
	MaskedResponseTexts []string `json:"masked_response_texts,omitempty"`
}
