// Package redis implements repository.Store on top of a Redis server.
//
// Key layout:
//
//	guardrails:mask:<request_id>       STRING  statecodec payload (plain JSON or
//	                                           encryption envelope), EX = masking TTL
//	guardrails:rules                   HASH    field = rule_id, value = JSON(rule.Rule)
//	guardrails:rules:disabled          SET     member = rule_id
//	guardrails:settings                STRING  JSON(models.GuardrailsSettings)
//	guardrails:audit:rec:<request_id>  STRING  JSON(models.AuditRecord), EX = audit TTL
//	guardrails:audit:idx               ZSET    score = Timestamp UnixMicro, member = request_id
package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository/statecodec"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
)

const (
	maskKeyPrefix     = "guardrails:mask:"
	rulesKey          = "guardrails:rules"
	disabledRulesKey  = "guardrails:rules:disabled"
	settingsKey       = "guardrails:settings"
	auditRecKeyPrefix = "guardrails:audit:rec:"
	auditIdxKey       = "guardrails:audit:idx"

	// auditListMaxRounds bounds the index scan of one filtered ListAuditRecords
	// call: filters are applied client-side, so a highly selective query over a
	// large window may return a short page with a NextCursor instead of scanning
	// the whole index.
	auditListMaxRounds = 10
	auditListMinBatch  = 100
)

// Config holds Redis connection parameters.
type Config struct {
	Addr     string
	Password string
	DB       int
}

// Store is a Redis-backed repository.Store implementation.
type Store struct {
	client     *goredis.Client
	maskingTTL time.Duration
	auditTTL   time.Duration
	codec      statecodec.Codec
}

// New creates a Redis store with its own client. codec controls masking-state
// serialization (statecodec.Plain or an encrypting codec); nil means plain.
func New(cfg Config, maskingTTL, auditTTL time.Duration, codec statecodec.Codec) *Store {
	if codec == nil {
		codec = statecodec.Plain()
	}
	client := goredis.NewClient(&goredis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})
	return &Store{client: client, maskingTTL: maskingTTL, auditTTL: auditTTL, codec: codec}
}

func maskKey(requestID string) string     { return maskKeyPrefix + requestID }
func auditRecKey(requestID string) string { return auditRecKeyPrefix + requestID }

func (s *Store) PutMaskingState(ctx context.Context, requestID string, st models.MaskingState) error {
	payload, err := s.codec.Encode(st)
	if err != nil {
		return fmt.Errorf("encode masking state: %w", err)
	}
	if err := s.client.Set(ctx, maskKey(requestID), payload, s.maskingTTL).Err(); err != nil {
		return fmt.Errorf("redis set masking state: %w", err)
	}
	return nil
}

func (s *Store) GetMaskingState(ctx context.Context, requestID string) (models.MaskingState, error) {
	payload, err := s.client.Get(ctx, maskKey(requestID)).Bytes()
	if errors.Is(err, goredis.Nil) {
		return models.MaskingState{}, repository.ErrNotFound
	}
	if err != nil {
		return models.MaskingState{}, fmt.Errorf("redis get masking state: %w", err)
	}
	st, err := s.codec.Decode(payload)
	if err != nil {
		return models.MaskingState{}, fmt.Errorf("decode masking state: %w", err)
	}
	return st, nil
}

func (s *Store) DeleteMaskingState(ctx context.Context, requestID string) error {
	if err := s.client.Del(ctx, maskKey(requestID)).Err(); err != nil {
		return fmt.Errorf("redis delete masking state: %w", err)
	}
	return nil
}

func (s *Store) PutAuditRecord(ctx context.Context, rec models.AuditRecord) error {
	// The ZSET score is a float64: UnixNano loses precision there, UnixMicro
	// is exact. Truncate the record to microseconds so the JSON document and
	// the index score describe the same instant (matches postgres precision).
	rec.Timestamp = rec.Timestamp.Truncate(time.Microsecond)
	payload, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal audit record: %w", err)
	}
	pipe := s.client.Pipeline()
	pipe.Set(ctx, auditRecKey(rec.RequestID), payload, s.auditTTL)
	pipe.ZAdd(ctx, auditIdxKey, goredis.Z{Score: float64(rec.Timestamp.UnixMicro()), Member: rec.RequestID})
	// Self-trim the index on every write: drop entries older than the TTL
	// window so it never references long-expired record keys.
	trimBefore := time.Now().Add(-s.auditTTL).UnixMicro()
	pipe.ZRemRangeByScore(ctx, auditIdxKey, "-inf", "("+strconv.FormatInt(trimBefore, 10))
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis put audit record: %w", err)
	}
	return nil
}

func (s *Store) GetAuditRecord(ctx context.Context, requestID string) (models.AuditRecord, error) {
	payload, err := s.client.Get(ctx, auditRecKey(requestID)).Bytes()
	if errors.Is(err, goredis.Nil) {
		return models.AuditRecord{}, repository.ErrNotFound
	}
	if err != nil {
		return models.AuditRecord{}, fmt.Errorf("redis get audit record: %w", err)
	}
	var rec models.AuditRecord
	if err := json.Unmarshal(payload, &rec); err != nil {
		return models.AuditRecord{}, fmt.Errorf("unmarshal audit record: %w", err)
	}
	return rec, nil
}

func (s *Store) SetAuditResponseTexts(ctx context.Context, requestID string, texts []string) error {
	// Read-modify-write on the record STRING, preserving its remaining TTL
	// (KeepTTL) and leaving the index ZSET untouched (ts/id are unchanged).
	// Last-write-wins against a concurrent PutAuditRecord, matching the plain-
	// pipeline house style elsewhere in this backend.
	key := auditRecKey(requestID)
	payload, err := s.client.Get(ctx, key).Bytes()
	if errors.Is(err, goredis.Nil) {
		return repository.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("redis get audit record: %w", err)
	}
	var rec models.AuditRecord
	if err := json.Unmarshal(payload, &rec); err != nil {
		return fmt.Errorf("unmarshal audit record: %w", err)
	}
	rec.MaskedResponseTexts = texts
	updated, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal audit record: %w", err)
	}
	if err := s.client.Set(ctx, key, updated, goredis.KeepTTL).Err(); err != nil {
		return fmt.Errorf("redis set audit record: %w", err)
	}
	return nil
}

func (s *Store) ListAuditRecords(ctx context.Context, q repository.AuditQuery) (repository.AuditPage, error) {
	var cursorMicro int64
	var cursorID string
	hasCursor := q.Cursor != ""
	if hasCursor {
		afterTS, afterID, err := repository.DecodeAuditCursor(q.Cursor)
		if err != nil {
			return repository.AuditPage{}, err
		}
		cursorMicro, cursorID = afterTS.UnixMicro(), afterID
	}

	// Best-effort trim of index entries older than the retention window. The
	// index is otherwise only self-trimmed on write (PutAuditRecord), so after
	// a write-quiet period it can still reference record keys that have expired
	// via TTL. Trimming here keeps a read from spending its bounded rounds
	// scanning dead members. Failures are non-fatal — the MGet-nil skip below
	// still tolerates any residual stale members.
	trimBefore := time.Now().Add(-s.auditTTL).UnixMicro()
	_ = s.client.ZRemRangeByScore(ctx, auditIdxKey, "-inf", "("+strconv.FormatInt(trimBefore, 10)).Err()

	limit := q.ClampAuditLimit()
	batch := int64(max(limit+1, auditListMinBatch))

	// Keyset pagination by score across rounds (never a positional Offset): a
	// concurrent PutAuditRecord adding a higher-scored member would shift a
	// positional offset and cause a filtered listing to skip or duplicate
	// records mid-call. maxBound is the inclusive upper score for the next
	// round; seenAtBound holds the ids already examined at exactly that score,
	// so re-including the boundary score to catch same-microsecond ties does
	// not revisit them.
	maxBound := "+inf"
	if hasCursor {
		maxBound = strconv.FormatInt(cursorMicro, 10)
	}
	seenAtBound := map[string]struct{}{}

	var collected []models.AuditRecord
	var lastTS time.Time
	var lastID string
	exhausted := false

	for round := 0; round < auditListMaxRounds && len(collected) <= limit; round++ {
		zs, err := s.client.ZRevRangeByScoreWithScores(ctx, auditIdxKey, &goredis.ZRangeBy{
			Min: "-inf", Max: maxBound, Offset: 0, Count: batch,
		}).Result()
		if err != nil {
			return repository.AuditPage{}, fmt.Errorf("redis list audit index: %w", err)
		}
		if len(zs) == 0 {
			exhausted = true
			break
		}

		minScore := int64(zs[len(zs)-1].Score)
		progressed := false
		ids := make([]string, 0, len(zs))
		for _, z := range zs {
			id, _ := z.Member.(string)
			score := int64(z.Score)
			if _, seen := seenAtBound[id]; seen {
				continue // already examined at the boundary score in a prior round
			}
			progressed = true
			// lastTS/lastID track every examined member (even cursor-skipped),
			// so a page made entirely of cursor-tie skips still yields a
			// resumable NextCursor instead of dropping it.
			lastTS, lastID = time.UnixMicro(score).UTC(), id
			if hasCursor && score == cursorMicro && id >= cursorID {
				continue // at or before the cursor position in desc order
			}
			ids = append(ids, id)
		}

		if len(ids) > 0 {
			keys := make([]string, len(ids))
			for i, id := range ids {
				keys[i] = auditRecKey(id)
			}
			payloads, err := s.client.MGet(ctx, keys...).Result()
			if err != nil {
				return repository.AuditPage{}, fmt.Errorf("redis mget audit records: %w", err)
			}
			for _, p := range payloads {
				str, ok := p.(string)
				if !ok {
					continue // record expired between index read and MGET
				}
				var rec models.AuditRecord
				if err := json.Unmarshal([]byte(str), &rec); err != nil {
					return repository.AuditPage{}, fmt.Errorf("unmarshal audit record: %w", err)
				}
				if !q.Matches(rec) {
					continue
				}
				collected = append(collected, rec)
				if len(collected) > limit {
					break
				}
			}
		}

		if int64(len(zs)) < batch {
			exhausted = true
			break
		}
		// A full batch that advanced nothing means one score has more members
		// than a batch and they were all already seen — stop rather than spin.
		if !progressed {
			exhausted = true
			break
		}
		// Advance the window: next round re-includes minScore (inclusive) to
		// catch same-microsecond ties; remember which ids at that score were
		// returned so they are skipped next round.
		nextSeen := make(map[string]struct{})
		for _, z := range zs {
			if int64(z.Score) == minScore {
				if id, ok := z.Member.(string); ok {
					nextSeen[id] = struct{}{}
				}
			}
		}
		maxBound = strconv.FormatInt(minScore, 10)
		seenAtBound = nextSeen
	}

	page := repository.AuditPage{Records: collected}
	switch {
	case len(collected) > limit:
		page.Records = collected[:limit]
		last := page.Records[limit-1]
		page.NextCursor = repository.EncodeAuditCursor(last.Timestamp, last.RequestID)
	case !exhausted && lastID != "":
		// Scan budget hit before the page filled: resume from the last
		// examined index position (short page, documented behavior).
		page.NextCursor = repository.EncodeAuditCursor(lastTS, lastID)
	}
	return page, nil
}

func (s *Store) ListRules(ctx context.Context) ([]rule.Rule, error) {
	entries, err := s.client.HGetAll(ctx, rulesKey).Result()
	if err != nil {
		return nil, fmt.Errorf("redis list rules: %w", err)
	}
	rules := make([]rule.Rule, 0, len(entries))
	for id, payload := range entries {
		var r rule.Rule
		if err := json.Unmarshal([]byte(payload), &r); err != nil {
			return nil, fmt.Errorf("unmarshal rule %q: %w", id, err)
		}
		rules = append(rules, r)
	}
	return rules, nil
}

func (s *Store) GetRule(ctx context.Context, id string) (rule.Rule, error) {
	payload, err := s.client.HGet(ctx, rulesKey, id).Bytes()
	if errors.Is(err, goredis.Nil) {
		return rule.Rule{}, repository.ErrNotFound
	}
	if err != nil {
		return rule.Rule{}, fmt.Errorf("redis get rule: %w", err)
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
	// HSetNX is atomic: only the first creator of this field succeeds.
	created, err := s.client.HSetNX(ctx, rulesKey, r.ID, payload).Result()
	if err != nil {
		return fmt.Errorf("redis create rule: %w", err)
	}
	if !created {
		return repository.ErrAlreadyExists
	}
	return nil
}

func (s *Store) SaveRule(ctx context.Context, r rule.Rule) error {
	payload, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("marshal rule %q: %w", r.ID, err)
	}
	if err := s.client.HSet(ctx, rulesKey, r.ID, payload).Err(); err != nil {
		return fmt.Errorf("redis save rule: %w", err)
	}
	return nil
}

func (s *Store) DeleteRule(ctx context.Context, id string) error {
	deleted, err := s.client.HDel(ctx, rulesKey, id).Result()
	if err != nil {
		return fmt.Errorf("redis delete rule: %w", err)
	}
	if deleted == 0 {
		return repository.ErrNotFound
	}
	return nil
}

func (s *Store) ListDisabledRuleIDs(ctx context.Context) ([]string, error) {
	ids, err := s.client.SMembers(ctx, disabledRulesKey).Result()
	if err != nil {
		return nil, fmt.Errorf("redis list disabled rule ids: %w", err)
	}
	return ids, nil
}

func (s *Store) SetRuleDisabled(ctx context.Context, id string, disabled bool) error {
	var err error
	if disabled {
		err = s.client.SAdd(ctx, disabledRulesKey, id).Err()
	} else {
		err = s.client.SRem(ctx, disabledRulesKey, id).Err()
	}
	if err != nil {
		return fmt.Errorf("redis set rule disabled: %w", err)
	}
	return nil
}

func (s *Store) GetSettings(ctx context.Context) (*models.GuardrailsSettings, error) {
	payload, err := s.client.Get(ctx, settingsKey).Bytes()
	if errors.Is(err, goredis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("redis get settings: %w", err)
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
	if err := s.client.Set(ctx, settingsKey, payload, 0).Err(); err != nil {
		return fmt.Errorf("redis save settings: %w", err)
	}
	return nil
}

func (s *Store) SaveSettingsIfAbsent(ctx context.Context, gs models.GuardrailsSettings) (bool, error) {
	payload, err := json.Marshal(gs)
	if err != nil {
		return false, fmt.Errorf("marshal settings: %w", err)
	}
	// SET ... NX is atomic across replicas: only the first writer succeeds;
	// a lost race returns redis.Nil (key already present).
	err = s.client.SetArgs(ctx, settingsKey, payload, goredis.SetArgs{Mode: "NX"}).Err()
	if errors.Is(err, goredis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("redis save settings if absent: %w", err)
	}
	return true, nil
}

func (s *Store) Ping(ctx context.Context) error {
	return s.client.Ping(ctx).Err()
}

func (s *Store) Close() error {
	return s.client.Close()
}

var _ repository.Store = (*Store)(nil)
