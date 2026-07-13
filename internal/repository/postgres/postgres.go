// Package postgres implements repository.Store on top of PostgreSQL (pgx).
//
// Rules, settings and masking state are stored as JSONB documents so the
// SQL schema stays decoupled from the Go struct evolution. The bootstrap
// schema is embedded and applied at startup (idempotent DDL).
package postgres

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository/statecodec"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
)

//go:embed schema.sql
var schemaSQL string

const janitorInterval = time.Minute

// Store is a PostgreSQL-backed repository.Store implementation.
type Store struct {
	pool       *pgxpool.Pool
	maskingTTL time.Duration
	auditTTL   time.Duration
	codec      statecodec.Codec

	stopJanitor chan struct{}
	janitorDone chan struct{}
	closeOnce   sync.Once
}

// New connects to PostgreSQL, applies the bootstrap schema and starts the
// expired-entries janitor. Call Close to release everything. codec controls
// masking-state serialization (statecodec.Plain or an encrypting codec); nil
// means plain.
func New(ctx context.Context, dsn string, maskingTTL, auditTTL time.Duration, codec statecodec.Codec) (*Store, error) {
	if codec == nil {
		codec = statecodec.Plain()
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	if _, err := pool.Exec(ctx, schemaSQL); err != nil {
		pool.Close()
		return nil, fmt.Errorf("apply bootstrap schema: %w", err)
	}

	s := &Store{
		pool:        pool,
		maskingTTL:  maskingTTL,
		auditTTL:    auditTTL,
		codec:       codec,
		stopJanitor: make(chan struct{}),
		janitorDone: make(chan struct{}),
	}
	go s.janitor()
	return s, nil
}

func (s *Store) janitor() {
	defer close(s.janitorDone)
	ticker := time.NewTicker(janitorInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopJanitor:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			if _, err := s.pool.Exec(ctx, `DELETE FROM guardrails_masking_state WHERE expires_at < now()`); err != nil {
				slog.Warn("postgres store: failed to evict expired masking state", "error", err)
			}
			if _, err := s.pool.Exec(ctx, `DELETE FROM guardrails_audit WHERE expires_at < now()`); err != nil {
				slog.Warn("postgres store: failed to evict expired audit records", "error", err)
			}
			cancel()
		}
	}
}

func (s *Store) PutMaskingState(ctx context.Context, requestID string, st models.MaskingState) error {
	payload, err := s.codec.Encode(st)
	if err != nil {
		return fmt.Errorf("encode masking state: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO guardrails_masking_state (request_id, state, expires_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (request_id) DO UPDATE SET state = EXCLUDED.state, expires_at = EXCLUDED.expires_at`,
		requestID, payload, time.Now().Add(s.maskingTTL))
	if err != nil {
		return fmt.Errorf("postgres put masking state: %w", err)
	}
	return nil
}

func (s *Store) GetMaskingState(ctx context.Context, requestID string) (models.MaskingState, error) {
	var payload []byte
	err := s.pool.QueryRow(ctx, `
		SELECT state FROM guardrails_masking_state
		WHERE request_id = $1 AND expires_at > now()`, requestID).Scan(&payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.MaskingState{}, repository.ErrNotFound
	}
	if err != nil {
		return models.MaskingState{}, fmt.Errorf("postgres get masking state: %w", err)
	}
	st, err := s.codec.Decode(payload)
	if err != nil {
		return models.MaskingState{}, fmt.Errorf("decode masking state: %w", err)
	}
	return st, nil
}

func (s *Store) DeleteMaskingState(ctx context.Context, requestID string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM guardrails_masking_state WHERE request_id = $1`, requestID); err != nil {
		return fmt.Errorf("postgres delete masking state: %w", err)
	}
	return nil
}

func (s *Store) PutAuditRecord(ctx context.Context, rec models.AuditRecord) error {
	// TIMESTAMPTZ holds microseconds; truncate before marshaling so the ts
	// column and the JSONB record agree — otherwise keyset pagination
	// comparing a JSON-precision cursor against the column could repeat rows.
	rec.Timestamp = rec.Timestamp.Truncate(time.Microsecond)
	payload, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal audit record: %w", err)
	}
	dataTypes := make([]int32, len(rec.TriggeredDataTypes))
	for i, dt := range rec.TriggeredDataTypes {
		dataTypes[i] = int32(dt)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO guardrails_audit (request_id, ts, model, path, rule_ids, data_types, record, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (request_id) DO UPDATE SET
			ts = EXCLUDED.ts, model = EXCLUDED.model, path = EXCLUDED.path,
			rule_ids = EXCLUDED.rule_ids, data_types = EXCLUDED.data_types,
			record = EXCLUDED.record, expires_at = EXCLUDED.expires_at`,
		rec.RequestID, rec.Timestamp, rec.Model, rec.Path,
		rec.TriggeredRuleIDs, dataTypes, payload, time.Now().Add(s.auditTTL))
	if err != nil {
		return fmt.Errorf("postgres put audit record: %w", err)
	}
	return nil
}

func (s *Store) GetAuditRecord(ctx context.Context, requestID string) (models.AuditRecord, error) {
	var payload []byte
	err := s.pool.QueryRow(ctx, `
		SELECT record FROM guardrails_audit
		WHERE request_id = $1 AND expires_at > now()`, requestID).Scan(&payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.AuditRecord{}, repository.ErrNotFound
	}
	if err != nil {
		return models.AuditRecord{}, fmt.Errorf("postgres get audit record: %w", err)
	}
	var rec models.AuditRecord
	if err := json.Unmarshal(payload, &rec); err != nil {
		return models.AuditRecord{}, fmt.Errorf("unmarshal audit record: %w", err)
	}
	return rec, nil
}

func (s *Store) SetAuditResponseTexts(ctx context.Context, requestID string, texts []string) error {
	// In-place JSONB update of the masked_response_texts key; the denormalized
	// index columns (ts/model/path/rule_ids/data_types) are unaffected. No DDL.
	value, err := json.Marshal(texts)
	if err != nil {
		return fmt.Errorf("marshal response texts: %w", err)
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE guardrails_audit
		SET record = jsonb_set(record, '{masked_response_texts}', $2::jsonb, true)
		WHERE request_id = $1 AND expires_at > now()`, requestID, value)
	if err != nil {
		return fmt.Errorf("postgres set audit response texts: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return repository.ErrNotFound
	}
	return nil
}

func (s *Store) ListAuditRecords(ctx context.Context, q repository.AuditQuery) (repository.AuditPage, error) {
	sql := `SELECT record FROM guardrails_audit WHERE expires_at > now()`
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if q.Model != "" {
		sql += ` AND model = ` + arg(q.Model)
	}
	if q.Path != "" {
		sql += ` AND path = ` + arg(q.Path)
	}
	if q.RuleID != "" {
		sql += ` AND ` + arg(q.RuleID) + ` = ANY(rule_ids)`
	}
	if q.DataType != models.DataTypeUNSPECIFIED {
		sql += ` AND ` + arg(int32(q.DataType)) + ` = ANY(data_types)`
	}
	if !q.Since.IsZero() {
		sql += ` AND ts >= ` + arg(q.Since)
	}
	if !q.Until.IsZero() {
		sql += ` AND ts < ` + arg(q.Until)
	}
	if q.Cursor != "" {
		afterTS, afterID, err := repository.DecodeAuditCursor(q.Cursor)
		if err != nil {
			return repository.AuditPage{}, err
		}
		sql += ` AND (ts, request_id) < (` + arg(afterTS) + `, ` + arg(afterID) + `)`
	}
	limit := q.ClampAuditLimit()
	// limit+1 decides whether a next page exists without a second query.
	sql += ` ORDER BY ts DESC, request_id DESC LIMIT ` + arg(limit+1)

	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return repository.AuditPage{}, fmt.Errorf("postgres list audit records: %w", err)
	}
	defer rows.Close()

	var records []models.AuditRecord
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return repository.AuditPage{}, fmt.Errorf("postgres scan audit record: %w", err)
		}
		var rec models.AuditRecord
		if err := json.Unmarshal(payload, &rec); err != nil {
			return repository.AuditPage{}, fmt.Errorf("unmarshal audit record: %w", err)
		}
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return repository.AuditPage{}, fmt.Errorf("postgres list audit records: %w", err)
	}

	page := repository.AuditPage{Records: records}
	if len(records) > limit {
		page.Records = records[:limit]
		last := page.Records[limit-1]
		page.NextCursor = repository.EncodeAuditCursor(last.Timestamp, last.RequestID)
	}
	return page, nil
}

func (s *Store) ListRules(ctx context.Context) ([]rule.Rule, error) {
	rows, err := s.pool.Query(ctx, `SELECT rule FROM guardrails_rules`)
	if err != nil {
		return nil, fmt.Errorf("postgres list rules: %w", err)
	}
	defer rows.Close()

	var rules []rule.Rule
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("postgres scan rule: %w", err)
		}
		var r rule.Rule
		if err := json.Unmarshal(payload, &r); err != nil {
			return nil, fmt.Errorf("unmarshal rule: %w", err)
		}
		rules = append(rules, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres list rules: %w", err)
	}
	return rules, nil
}

func (s *Store) GetRule(ctx context.Context, id string) (rule.Rule, error) {
	var payload []byte
	err := s.pool.QueryRow(ctx, `SELECT rule FROM guardrails_rules WHERE rule_id = $1`, id).Scan(&payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return rule.Rule{}, repository.ErrNotFound
	}
	if err != nil {
		return rule.Rule{}, fmt.Errorf("postgres get rule: %w", err)
	}
	var r rule.Rule
	if err := json.Unmarshal(payload, &r); err != nil {
		return rule.Rule{}, fmt.Errorf("unmarshal rule %q: %w", id, err)
	}
	return r, nil
}

func (s *Store) CreateRule(ctx context.Context, r rule.Rule) error {
	payload, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("marshal rule %q: %w", r.ID, err)
	}
	// DO NOTHING + RowsAffected: the insert lands only when rule_id is absent,
	// atomically even under concurrent creators.
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO guardrails_rules (rule_id, rule)
		VALUES ($1, $2)
		ON CONFLICT (rule_id) DO NOTHING`,
		r.ID, payload)
	if err != nil {
		return fmt.Errorf("postgres create rule: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return repository.ErrAlreadyExists
	}
	return nil
}

func (s *Store) SaveRule(ctx context.Context, r rule.Rule) error {
	payload, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("marshal rule %q: %w", r.ID, err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO guardrails_rules (rule_id, rule)
		VALUES ($1, $2)
		ON CONFLICT (rule_id) DO UPDATE SET rule = EXCLUDED.rule, updated_at = now()`,
		r.ID, payload)
	if err != nil {
		return fmt.Errorf("postgres save rule: %w", err)
	}
	return nil
}

func (s *Store) DeleteRule(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM guardrails_rules WHERE rule_id = $1`, id)
	if err != nil {
		return fmt.Errorf("postgres delete rule: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return repository.ErrNotFound
	}
	return nil
}

func (s *Store) ListDisabledRuleIDs(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT rule_id FROM guardrails_disabled_rules`)
	if err != nil {
		return nil, fmt.Errorf("postgres list disabled rule ids: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("postgres scan disabled rule id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres list disabled rule ids: %w", err)
	}
	return ids, nil
}

func (s *Store) SetRuleDisabled(ctx context.Context, id string, disabled bool) error {
	var err error
	if disabled {
		_, err = s.pool.Exec(ctx, `
			INSERT INTO guardrails_disabled_rules (rule_id)
			VALUES ($1)
			ON CONFLICT (rule_id) DO NOTHING`, id)
	} else {
		_, err = s.pool.Exec(ctx, `DELETE FROM guardrails_disabled_rules WHERE rule_id = $1`, id)
	}
	if err != nil {
		return fmt.Errorf("postgres set rule disabled: %w", err)
	}
	return nil
}

func (s *Store) GetSettings(ctx context.Context) (*models.GuardrailsSettings, error) {
	var payload []byte
	err := s.pool.QueryRow(ctx, `SELECT settings FROM guardrails_settings WHERE id = 1`).Scan(&payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres get settings: %w", err)
	}
	var gs models.GuardrailsSettings
	if err := json.Unmarshal(payload, &gs); err != nil {
		return nil, fmt.Errorf("unmarshal settings: %w", err)
	}
	return &gs, nil
}

func (s *Store) SaveSettings(ctx context.Context, gs models.GuardrailsSettings) error {
	payload, err := json.Marshal(gs)
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO guardrails_settings (id, settings)
		VALUES (1, $1)
		ON CONFLICT (id) DO UPDATE SET settings = EXCLUDED.settings, updated_at = now()`,
		payload)
	if err != nil {
		return fmt.Errorf("postgres save settings: %w", err)
	}
	return nil
}

func (s *Store) SaveSettingsIfAbsent(ctx context.Context, gs models.GuardrailsSettings) (bool, error) {
	payload, err := json.Marshal(gs)
	if err != nil {
		return false, fmt.Errorf("marshal settings: %w", err)
	}
	// DO NOTHING + RowsAffected: the write lands only when row id=1 is absent,
	// atomically across concurrent replica boots.
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO guardrails_settings (id, settings)
		VALUES (1, $1)
		ON CONFLICT (id) DO NOTHING`,
		payload)
	if err != nil {
		return false, fmt.Errorf("postgres save settings if absent: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		close(s.stopJanitor)
		<-s.janitorDone
		s.pool.Close()
	})
	return nil
}

var _ repository.Store = (*Store)(nil)
