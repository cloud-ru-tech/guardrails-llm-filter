// Package repository defines the persistence abstraction shared by masking
// state, custom rules, global settings and the masking audit trail. One
// backend (selected via GUARDRAILS_STORE_BACKEND) serves all roles.
package repository

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
)

// ErrNotFound is returned when the requested entity does not exist.
var ErrNotFound = errors.New("store: not found")

// ErrAlreadyExists is returned by create-if-absent operations (e.g. CreateRule)
// when an entity with the same identity already exists.
var ErrAlreadyExists = errors.New("store: already exists")

// ErrUndecryptable is returned when a persisted masking-state record cannot
// be decoded: it is encrypted with a different key, tampered with, or was
// written with encryption enabled while it is now disabled. Callers on the
// data path treat it like any other store error (log-only, fail-open).
var ErrUndecryptable = errors.New("store: undecryptable masking state")

// ErrBadCursor is returned when an audit list cursor cannot be decoded.
var ErrBadCursor = errors.New("store: invalid cursor")

// MaskingStateStore persists per-request masking state between the request
// and response phases.
//
// Lifecycle contract:
//   - Put: end of the request-body phase, only when replacements are non-empty.
//   - Get: response phase, only when the in-process state is empty (i.e. a
//     different replica handled the request phase).
//   - Delete: best-effort at stream end.
//   - TTL is the safety net for missed deletes and must exceed the longest
//     expected streaming response.
//
// SECURITY: MaskingState carries the original (unmasked) sensitive values.
// External backends must be access-controlled; entries are short-lived by TTL.
// Optional at-rest encryption (GUARDRAILS_STORE_ENCRYPTION_*) protects the
// serialized state in external backends; see internal/repository/statecodec.
// All errors on the data path are treated as log-only by callers (fail-open).
type MaskingStateStore interface {
	PutMaskingState(ctx context.Context, requestID string, st models.MaskingState) error
	GetMaskingState(ctx context.Context, requestID string) (models.MaskingState, error)
	DeleteMaskingState(ctx context.Context, requestID string) error
}

// RuleStore persists custom regex rules created via the configuration API,
// plus the set of rule IDs disabled via the API (builtin or custom).
type RuleStore interface {
	ListRules(ctx context.Context) ([]rule.Rule, error)
	GetRule(ctx context.Context, id string) (rule.Rule, error)
	// CreateRule inserts a rule only when its rule_id is not already present,
	// returning ErrAlreadyExists otherwise. Atomic, so concurrent creators of
	// the same id cannot both succeed (no last-write-wins clobber).
	CreateRule(ctx context.Context, r rule.Rule) error
	// SaveRule upserts a rule by its rule_id.
	SaveRule(ctx context.Context, r rule.Rule) error
	DeleteRule(ctx context.Context, id string) error
	// ListDisabledRuleIDs returns the IDs currently disabled via the
	// configuration API. The set may contain stale IDs that match no
	// existing rule; callers must ignore them.
	ListDisabledRuleIDs(ctx context.Context) ([]string, error)
	// SetRuleDisabled adds (disabled=true) or removes (disabled=false) an ID
	// from the disabled set. Idempotent; never returns ErrNotFound.
	SetRuleDisabled(ctx context.Context, id string, disabled bool) error
}

// SettingsStore persists the global guardrails settings.
type SettingsStore interface {
	// GetSettings returns (nil, nil) when settings were never persisted.
	GetSettings(ctx context.Context) (*models.GuardrailsSettings, error)
	SaveSettings(ctx context.Context, s models.GuardrailsSettings) error
	// SaveSettingsIfAbsent persists s only when no settings exist yet. It
	// reports whether this call performed the write (false means another
	// writer — a concurrent replica or an API update — got there first, and
	// the caller should re-read rather than assume its own value took hold).
	// Used for seed-once semantics so a booting replica cannot clobber
	// freshly persisted settings with its env defaults.
	SaveSettingsIfAbsent(ctx context.Context, s models.GuardrailsSettings) (written bool, err error)
}

// Audit listing page-size bounds; callers clamp Limit with ClampAuditLimit.
const (
	DefaultAuditPageSize = 50
	MaxAuditPageSize     = 500
)

// AuditQuery filters and paginates audit listing. Zero values mean "any".
type AuditQuery struct {
	Model    string          // exact match
	Path     string          // exact match
	RuleID   string          // record's TriggeredRuleIDs contains it
	DataType models.DataType // record's TriggeredDataTypes contains it (UNSPECIFIED = any)
	Since    time.Time       // inclusive lower bound on Timestamp
	Until    time.Time       // exclusive upper bound
	Limit    int             // page size; 0 = DefaultAuditPageSize, capped at MaxAuditPageSize
	Cursor   string          // opaque cursor from a previous AuditPage ("" = first page)
}

// ClampAuditLimit resolves the effective page size for a query.
func (q AuditQuery) ClampAuditLimit() int {
	switch {
	case q.Limit <= 0:
		return DefaultAuditPageSize
	case q.Limit > MaxAuditPageSize:
		return MaxAuditPageSize
	default:
		return q.Limit
	}
}

// Matches reports whether the record passes the query's field filters
// (Model/Path/RuleID/DataType/Since/Until). Backends without server-side
// filtering apply it in process; cursor/limit handling stays backend-specific.
func (q AuditQuery) Matches(rec models.AuditRecord) bool {
	if q.Model != "" && rec.Model != q.Model {
		return false
	}
	if q.Path != "" && rec.Path != q.Path {
		return false
	}
	if q.RuleID != "" && !slices.Contains(rec.TriggeredRuleIDs, q.RuleID) {
		return false
	}
	if q.DataType != models.DataTypeUNSPECIFIED && !slices.Contains(rec.TriggeredDataTypes, q.DataType) {
		return false
	}
	if !q.Since.IsZero() && rec.Timestamp.Before(q.Since) {
		return false
	}
	if !q.Until.IsZero() && !rec.Timestamp.Before(q.Until) {
		return false
	}
	return true
}

// AuditPage is one page of an audit listing.
type AuditPage struct {
	Records    []models.AuditRecord
	NextCursor string // "" when the listing is exhausted
}

// AuditStore persists per-request masking audit records.
//
// Contract:
//   - PutAuditRecord upserts by RequestID (an Envoy retry reusing the
//     x-request-id overwrites the previous record — last write wins).
//   - Listing order is (Timestamp desc, RequestID desc); pagination is
//     keyset-based via the opaque cursor.
//   - Records are retained for the configured audit TTL.
//
// SECURITY: records omit original sensitive values by default. Optional
// MaskedTexts/MaskedResponseTexts carry user prompt/response content, and the
// opt-in AuditReplacement.Original (GUARDRAILS_AUDIT_STORE_ORIGINAL_TEXTS)
// carries raw originals — all must be access-controlled.
type AuditStore interface {
	PutAuditRecord(ctx context.Context, rec models.AuditRecord) error
	GetAuditRecord(ctx context.Context, requestID string) (models.AuditRecord, error)
	ListAuditRecords(ctx context.Context, q AuditQuery) (AuditPage, error)
	// SetAuditResponseTexts sets MaskedResponseTexts on an existing record by
	// request ID (best-effort response-phase enrichment). Returns ErrNotFound
	// when no live record exists for the ID (e.g. the request-phase write has
	// not landed yet, or the record expired); callers treat that as a no-op.
	SetAuditResponseTexts(ctx context.Context, requestID string, texts []string) error
}

// Store is the full persistence backend.
type Store interface {
	MaskingStateStore
	RuleStore
	SettingsStore
	AuditStore

	Ping(ctx context.Context) error
	Close() error
}
