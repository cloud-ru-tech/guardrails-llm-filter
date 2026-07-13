// Package memory implements repository.Store in process memory.
//
// It is the default backend: correct for single-replica deployments because
// one ServeHTTP call runs the whole mask→forward→demask cycle in-process.
// Custom rules and settings do not survive a restart — env defaults and the
// YAML rule files are the durable baseline.
package memory

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
)

const janitorInterval = time.Minute

type maskEntry struct {
	state     models.MaskingState
	expiresAt time.Time
}

type auditEntry struct {
	rec       models.AuditRecord
	expiresAt time.Time
}

// Store is an in-memory repository.Store implementation.
type Store struct {
	maskingTTL      time.Duration
	auditTTL        time.Duration
	auditMaxEntries int

	mu            sync.RWMutex
	masking       map[string]maskEntry
	audit         map[string]auditEntry
	rules         map[string]rule.Rule
	disabledRules map[string]struct{}
	settings      *models.GuardrailsSettings

	stopJanitor chan struct{}
	janitorDone chan struct{}
	closeOnce   sync.Once
}

// New creates a memory store and starts a janitor goroutine that evicts
// expired masking and audit entries. auditMaxEntries caps the audit map
// (0 = unlimited); the oldest record is evicted when the cap is hit.
// Call Close to stop the janitor.
func New(maskingTTL, auditTTL time.Duration, auditMaxEntries int) *Store {
	s := &Store{
		maskingTTL:      maskingTTL,
		auditTTL:        auditTTL,
		auditMaxEntries: auditMaxEntries,
		masking:         make(map[string]maskEntry),
		audit:           make(map[string]auditEntry),
		rules:           make(map[string]rule.Rule),
		disabledRules:   make(map[string]struct{}),
		stopJanitor:     make(chan struct{}),
		janitorDone:     make(chan struct{}),
	}
	go s.janitor()
	return s
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
			now := time.Now()
			s.mu.Lock()
			for id, e := range s.masking {
				if e.expiresAt.Before(now) {
					delete(s.masking, id)
				}
			}
			for id, e := range s.audit {
				if e.expiresAt.Before(now) {
					delete(s.audit, id)
				}
			}
			s.mu.Unlock()
		}
	}
}

func (s *Store) PutMaskingState(_ context.Context, requestID string, st models.MaskingState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.masking[requestID] = maskEntry{state: st, expiresAt: time.Now().Add(s.maskingTTL)}
	return nil
}

func (s *Store) GetMaskingState(_ context.Context, requestID string) (models.MaskingState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.masking[requestID]
	if !ok || e.expiresAt.Before(time.Now()) {
		return models.MaskingState{}, repository.ErrNotFound
	}
	return e.state, nil
}

func (s *Store) DeleteMaskingState(_ context.Context, requestID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.masking, requestID)
	return nil
}

func (s *Store) PutAuditRecord(_ context.Context, rec models.AuditRecord) error {
	// All backends store audit timestamps with microsecond precision
	// (postgres TIMESTAMPTZ / redis ZSET score); mirror that here so the
	// wire behavior is backend-independent.
	rec.Timestamp = rec.Timestamp.Truncate(time.Microsecond)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.audit[rec.RequestID]; !exists && s.auditMaxEntries > 0 && len(s.audit) >= s.auditMaxEntries {
		s.evictOldestAuditLocked()
	}
	s.audit[rec.RequestID] = auditEntry{rec: rec, expiresAt: time.Now().Add(s.auditTTL)}
	return nil
}

// evictOldestAuditLocked removes the entry with the smallest
// (Timestamp, RequestID). Linear scan is fine: it runs only when the cap is
// hit, and the map is bounded by that same cap.
func (s *Store) evictOldestAuditLocked() {
	var oldestID string
	var oldest auditEntry
	for id, e := range s.audit {
		if oldestID == "" || e.rec.Timestamp.Before(oldest.rec.Timestamp) ||
			(e.rec.Timestamp.Equal(oldest.rec.Timestamp) && id < oldestID) {
			oldestID, oldest = id, e
		}
	}
	if oldestID != "" {
		delete(s.audit, oldestID)
	}
}

func (s *Store) GetAuditRecord(_ context.Context, requestID string) (models.AuditRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.audit[requestID]
	if !ok || e.expiresAt.Before(time.Now()) {
		return models.AuditRecord{}, repository.ErrNotFound
	}
	return e.rec, nil
}

func (s *Store) SetAuditResponseTexts(_ context.Context, requestID string, texts []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.audit[requestID]
	if !ok || e.expiresAt.Before(time.Now()) {
		return repository.ErrNotFound
	}
	e.rec.MaskedResponseTexts = texts
	s.audit[requestID] = e // preserve expiresAt
	return nil
}

func (s *Store) ListAuditRecords(_ context.Context, q repository.AuditQuery) (repository.AuditPage, error) {
	var afterTS time.Time
	var afterID string
	if q.Cursor != "" {
		var err error
		afterTS, afterID, err = repository.DecodeAuditCursor(q.Cursor)
		if err != nil {
			return repository.AuditPage{}, err
		}
	}

	now := time.Now()
	s.mu.RLock()
	matched := make([]models.AuditRecord, 0, len(s.audit))
	for _, e := range s.audit {
		if e.expiresAt.Before(now) || !q.Matches(e.rec) {
			continue
		}
		matched = append(matched, e.rec)
	}
	s.mu.RUnlock()

	// Order contract: (Timestamp desc, RequestID desc).
	slices.SortFunc(matched, func(a, b models.AuditRecord) int {
		if c := b.Timestamp.Compare(a.Timestamp); c != 0 {
			return c
		}
		return strings.Compare(b.RequestID, a.RequestID)
	})

	if q.Cursor != "" {
		matched = slices.DeleteFunc(matched, func(r models.AuditRecord) bool {
			if r.Timestamp.After(afterTS) {
				return true
			}
			return r.Timestamp.Equal(afterTS) && r.RequestID >= afterID
		})
	}

	limit := q.ClampAuditLimit()
	page := repository.AuditPage{Records: matched}
	if len(matched) > limit {
		page.Records = matched[:limit]
		last := page.Records[limit-1]
		page.NextCursor = repository.EncodeAuditCursor(last.Timestamp, last.RequestID)
	}
	return page, nil
}

func (s *Store) ListRules(_ context.Context) ([]rule.Rule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Collect(maps.Values(s.rules)), nil
}

func (s *Store) GetRule(_ context.Context, id string) (rule.Rule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.rules[id]
	if !ok {
		return rule.Rule{}, repository.ErrNotFound
	}
	return r, nil
}

func (s *Store) CreateRule(_ context.Context, r rule.Rule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rules[r.ID]; ok {
		return repository.ErrAlreadyExists
	}
	s.rules[r.ID] = r
	return nil
}

func (s *Store) SaveRule(_ context.Context, r rule.Rule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rules[r.ID] = r
	return nil
}

func (s *Store) DeleteRule(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rules[id]; !ok {
		return repository.ErrNotFound
	}
	delete(s.rules, id)
	return nil
}

func (s *Store) ListDisabledRuleIDs(_ context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Collect(maps.Keys(s.disabledRules)), nil
}

func (s *Store) SetRuleDisabled(_ context.Context, id string, disabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if disabled {
		s.disabledRules[id] = struct{}{}
	} else {
		delete(s.disabledRules, id)
	}
	return nil
}

func (s *Store) GetSettings(_ context.Context) (*models.GuardrailsSettings, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.settings == nil {
		return nil, nil
	}
	cp := *s.settings
	cp.DataTypes = slices.Clone(s.settings.DataTypes)
	return &cp, nil
}

func (s *Store) SaveSettings(_ context.Context, gs models.GuardrailsSettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	gs.DataTypes = slices.Clone(gs.DataTypes)
	s.settings = &gs
	return nil
}

func (s *Store) SaveSettingsIfAbsent(_ context.Context, gs models.GuardrailsSettings) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settings != nil {
		return false, nil
	}
	gs.DataTypes = slices.Clone(gs.DataTypes)
	s.settings = &gs
	return true, nil
}

func (s *Store) Ping(_ context.Context) error { return nil }

func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		close(s.stopJanitor)
		<-s.janitorDone
	})
	return nil
}

var _ repository.Store = (*Store)(nil)
