package audit

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
)

type fakeAuditStore struct {
	mu      sync.Mutex
	records []models.AuditRecord
	err     error
}

func (s *fakeAuditStore) PutAuditRecord(_ context.Context, rec models.AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.records = append(s.records, rec)
	return nil
}

func (s *fakeAuditStore) SetAuditResponseTexts(_ context.Context, requestID string, texts []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	for i := range s.records {
		if s.records[i].RequestID == requestID {
			s.records[i].MaskedResponseTexts = texts
			return nil
		}
	}
	return repository.ErrNotFound
}

func (s *fakeAuditStore) last(t *testing.T) models.AuditRecord {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	require.NotEmpty(t, s.records)
	return s.records[len(s.records)-1]
}

func (s *fakeAuditStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records)
}

type fakeResolver struct {
	rules []rule.Rule
}

func (r *fakeResolver) GetRulesByIDs(_ ...string) []rule.Rule { return r.rules }

func testMetadata() models.Metadata {
	return models.Metadata{
		RequestID: "req-1",
		Model:     "gpt-x",
		Path:      "/v1/chat/completions",
		Mode:      models.ModeDetect,
	}
}

func testState() models.MaskingState {
	return models.MaskingState{
		TriggeredRuleIDs:   []string{"pii.email"},
		TriggeredDataTypes: []models.DataType{models.DataTypePERSONALDATA},
		Replacements: []models.Replacement{
			{RuleID: "pii.email", Original: "user@example.com", Placeholder: "<EMAIL_1>"},
		},
	}
}

func testResolver() *fakeResolver {
	return &fakeResolver{rules: []rule.Rule{
		{ID: "pii.email", DataType: int(models.DataTypePERSONALDATA)},
	}}
}

func waitRecorded(t *testing.T, st *fakeAuditStore) {
	t.Helper()
	require.Eventually(t, func() bool { return st.count() > 0 },
		time.Second, 5*time.Millisecond, "async audit write did not happen")
}

// blockingAuditStore holds each write until release is closed, so a test can
// observe in-flight writes.
type blockingAuditStore struct {
	release chan struct{}
	mu      sync.Mutex
	done    int
}

func (s *blockingAuditStore) PutAuditRecord(_ context.Context, _ models.AuditRecord) error {
	<-s.release
	s.mu.Lock()
	s.done++
	s.mu.Unlock()
	return nil
}

func (s *blockingAuditStore) SetAuditResponseTexts(_ context.Context, _ string, _ []string) error {
	<-s.release
	s.mu.Lock()
	s.done++
	s.mu.Unlock()
	return nil
}

func (s *blockingAuditStore) completed() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.done
}

func TestDrainWaitsForInFlightWrites(t *testing.T) {
	st := &blockingAuditStore{release: make(chan struct{})}
	r := New(st, testResolver(), false, false, "off", nil)

	const n = 10
	for range n {
		r.Record(testMetadata(), testState(), nil)
	}

	// Drain must not return while writes are blocked.
	drained := make(chan struct{})
	go func() {
		r.Drain(context.Background())
		close(drained)
	}()
	select {
	case <-drained:
		t.Fatal("Drain returned while writes were still in flight")
	case <-time.After(50 * time.Millisecond):
	}

	close(st.release) // let the writes finish
	require.Eventually(t, func() bool {
		select {
		case <-drained:
			return true
		default:
			return false
		}
	}, time.Second, 5*time.Millisecond, "Drain did not return after writes completed")
	assert.Equal(t, n, st.completed())
}

func TestRecordWorksAfterDrain(t *testing.T) {
	st := &fakeAuditStore{}
	r := New(st, testResolver(), false, false, "off", nil)

	r.Record(testMetadata(), testState(), nil)
	waitRecorded(t, st)

	// Drain releases its acquired slots, so the recorder must keep accepting
	// writes afterwards (not silently drop them by leaving the semaphore full).
	r.Drain(context.Background())

	before := st.count()
	r.Record(testMetadata(), testState(), nil)
	require.Eventually(t, func() bool { return st.count() > before },
		time.Second, 5*time.Millisecond, "Record dropped after Drain (semaphore left full)")
}

func TestDrainRespectsContext(t *testing.T) {
	st := &blockingAuditStore{release: make(chan struct{})}
	r := New(st, testResolver(), false, false, "off", nil)
	r.Record(testMetadata(), testState(), nil) // never released

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() { r.Drain(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Drain ignored context deadline")
	}
	close(st.release)
}

func TestRecordWritesAsync(t *testing.T) {
	st := &fakeAuditStore{}
	r := New(st, testResolver(), false, false, "off", nil)

	r.Record(testMetadata(), testState(), []string{"masked <EMAIL_1>"})
	waitRecorded(t, st)

	rec := st.last(t)
	assert.Equal(t, "req-1", rec.RequestID)
	assert.Equal(t, "gpt-x", rec.Model)
	assert.Equal(t, "/v1/chat/completions", rec.Path)
	assert.Equal(t, []string{"pii.email"}, rec.TriggeredRuleIDs)
	assert.Equal(t, []models.DataType{models.DataTypePERSONALDATA}, rec.TriggeredDataTypes)
	require.Len(t, rec.Replacements, 1)
	assert.Equal(t, models.AuditReplacement{
		RuleID:      "pii.email",
		DataType:    models.DataTypePERSONALDATA,
		Placeholder: "<EMAIL_1>",
	}, rec.Replacements[0])
	assert.WithinDuration(t, time.Now(), rec.Timestamp, time.Minute)
	assert.Nil(t, rec.MaskedTexts, "masked texts must be omitted when the flag is off")
	assert.Equal(t, string(models.ModeDetect), rec.Mode)
}

// The audit record must never carry the original sensitive value in any
// serialized form.
func TestRecordNeverContainsOriginal(t *testing.T) {
	st := &fakeAuditStore{}
	r := New(st, testResolver(), true, false, "off", nil)

	r.Record(testMetadata(), testState(), []string{"my mail is <EMAIL_1>"})
	waitRecorded(t, st)

	payload, err := json.Marshal(st.last(t))
	require.NoError(t, err)
	assert.NotContains(t, string(payload), "user@example.com")
}

func TestRecordStoresMaskedTextsWhenEnabled(t *testing.T) {
	st := &fakeAuditStore{}
	r := New(st, testResolver(), true, false, "off", nil)

	r.Record(testMetadata(), testState(), []string{"my mail is <EMAIL_1>"})
	waitRecorded(t, st)
	assert.Equal(t, []string{"my mail is <EMAIL_1>"}, st.last(t).MaskedTexts)
}

func TestRecordTruncatesMaskedTexts(t *testing.T) {
	st := &fakeAuditStore{}
	r := New(st, testResolver(), true, false, "off", nil)

	// Multi-byte runes across the cut boundary must not be split.
	huge := strings.Repeat("ю", maxMaskedTextBytes) // 2 bytes per rune
	r.Record(testMetadata(), testState(), []string{huge})
	waitRecorded(t, st)

	got := st.last(t).MaskedTexts[0]
	assert.LessOrEqual(t, len(got), maxMaskedTextBytes)
	for _, r := range got {
		assert.NotEqual(t, '�', r, "truncation must not split a rune")
	}
}

func TestRecordUnknownRuleFallsBackToUnspecified(t *testing.T) {
	st := &fakeAuditStore{}
	r := New(st, &fakeResolver{rules: nil}, false, false, "off", nil) // rule deleted from registry

	r.Record(testMetadata(), testState(), nil)
	waitRecorded(t, st)
	assert.Equal(t, models.DataTypeUNSPECIFIED, st.last(t).Replacements[0].DataType)
}

func TestRecordStoreErrorIsSwallowed(t *testing.T) {
	st := &fakeAuditStore{err: assert.AnError}
	r := New(st, testResolver(), false, false, "off", nil)

	// Must not panic or block; error is metric+log only.
	r.Record(testMetadata(), testState(), nil)
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, 0, st.count())
}

func TestTruncateUTF8(t *testing.T) {
	assert.Equal(t, "abc", truncateUTF8("abc", 10))
	assert.Equal(t, "ab", truncateUTF8("abcd", 2))
	assert.Equal(t, "ю", truncateUTF8("юю", 3), "must back off to the rune boundary")
	assert.Equal(t, "", truncateUTF8("ю", 1))
}
