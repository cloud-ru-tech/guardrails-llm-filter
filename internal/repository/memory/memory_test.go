package memory_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository/memory"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository/repositorytest"
)

func TestConformance(t *testing.T) {
	repositorytest.Run(t, func(t *testing.T) repository.Store {
		s := memory.New(15*time.Minute, 24*time.Hour, 0)
		t.Cleanup(func() { require.NoError(t, s.Close()) })
		return s
	}, repositorytest.Options{})
}

// TTL expiry is covered separately with a tiny TTL because the memory store
// checks expiry lazily on Get.
func TestMaskingStateTTL(t *testing.T) {
	s := memory.New(time.Millisecond, time.Minute, 0)
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	ctx := context.Background()
	require.NoError(t, s.PutMaskingState(ctx, "req", models.MaskingState{
		TriggeredRuleIDs: []string{"email"},
	}))

	require.Eventually(t, func() bool {
		_, err := s.GetMaskingState(ctx, "req")
		return err == repository.ErrNotFound
	}, time.Second, 5*time.Millisecond)
}

func TestAuditTTL(t *testing.T) {
	s := memory.New(time.Minute, time.Millisecond, 0)
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	ctx := context.Background()
	require.NoError(t, s.PutAuditRecord(ctx, models.AuditRecord{
		RequestID: "req",
		Timestamp: time.Now().UTC(),
	}))

	require.Eventually(t, func() bool {
		_, err := s.GetAuditRecord(ctx, "req")
		return err == repository.ErrNotFound
	}, time.Second, 5*time.Millisecond)

	page, err := s.ListAuditRecords(ctx, repository.AuditQuery{})
	require.NoError(t, err)
	require.Empty(t, page.Records)
}

func TestAuditMaxEntriesEviction(t *testing.T) {
	const cap = 3
	s := memory.New(time.Minute, time.Hour, cap)
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	ctx := context.Background()
	base := time.Now().UTC().Add(-time.Minute)
	for i := range cap + 2 {
		require.NoError(t, s.PutAuditRecord(ctx, models.AuditRecord{
			RequestID: fmt.Sprintf("req-%d", i),
			Timestamp: base.Add(time.Duration(i) * time.Second),
		}))
	}

	page, err := s.ListAuditRecords(ctx, repository.AuditQuery{})
	require.NoError(t, err)
	require.Len(t, page.Records, cap)
	// Oldest two evicted, newest kept.
	require.Equal(t, "req-4", page.Records[0].RequestID)
	require.Equal(t, "req-2", page.Records[2].RequestID)

	// Re-put of an existing id must not evict (upsert, not insert).
	require.NoError(t, s.PutAuditRecord(ctx, models.AuditRecord{
		RequestID: "req-4",
		Timestamp: base.Add(10 * time.Second),
	}))
	page, err = s.ListAuditRecords(ctx, repository.AuditQuery{})
	require.NoError(t, err)
	require.Len(t, page.Records, cap)
}

func TestCloseIsIdempotent(t *testing.T) {
	s := memory.New(time.Minute, time.Minute, 0)
	require.NoError(t, s.Close())
	require.NoError(t, s.Close())
}
