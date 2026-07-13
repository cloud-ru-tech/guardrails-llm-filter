package registry

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
)

// reloadableRule builds a minimal valid rule for Build/Reloadable tests,
// reusing the shared testRule helper from registry_test.go.
func reloadableRule(id string, dataType int) rule.Rule {
	return testRule(id, dataType, "TEST")
}

func TestBuild(t *testing.T) {
	t.Parallel()

	t.Run("valid rules", func(t *testing.T) {
		t.Parallel()
		reg, err := Build(reloadableRule("a", 1), reloadableRule("b", 2))
		require.NoError(t, err)
		assert.Len(t, reg.GetRuleIDsByDataTypes(1, 2), 2)
	})

	t.Run("duplicate rule_id", func(t *testing.T) {
		t.Parallel()
		_, err := Build(reloadableRule("a", 1), reloadableRule("a", 2))
		require.ErrorContains(t, err, "duplicate rule_id")
	})

	t.Run("invalid regex", func(t *testing.T) {
		t.Parallel()
		bad := reloadableRule("bad", 1)
		bad.Regex = "(unclosed"
		_, err := Build(bad)
		require.ErrorContains(t, err, "regex")
	})

	t.Run("unknown validator", func(t *testing.T) {
		t.Parallel()
		bad := reloadableRule("bad", 1)
		bad.Validators = []rule.ValidatorType{"no_such_validator"}
		_, err := Build(bad)
		require.ErrorContains(t, err, "unsupported validator")
	})

	t.Run("capture group out of range", func(t *testing.T) {
		t.Parallel()
		bad := reloadableRule("bad", 1)
		bad.Masking.CaptureGroups = []int{2}
		_, err := Build(bad)
		require.ErrorContains(t, err, "capture group")
	})
}

func TestReloadable_Swap(t *testing.T) {
	t.Parallel()

	first, err := Build(reloadableRule("first", 1))
	require.NoError(t, err)
	second, err := Build(reloadableRule("second", 1))
	require.NoError(t, err)

	r := NewReloadable(first)
	assert.Equal(t, []string{"first"}, r.GetRuleIDsByDataTypes(1))

	r.Swap(second)
	assert.Equal(t, []string{"second"}, r.GetRuleIDsByDataTypes(1))
}

// TestReloadable_ResolveForDataTypesPinsSnapshot verifies that
// ResolveForDataTypes never tears across a concurrent Swap: every returned
// rule ID resolves to a compiled rule from the same snapshot, so len(ids)
// always equals len(rules). It swaps between a snapshot that has the data
// type's rule and one that does not, so a torn read would surface as a
// mismatch. Run with -race.
func TestReloadable_ResolveForDataTypesPinsSnapshot(t *testing.T) {
	t.Parallel()

	withRule, err := Build(reloadableRule("rule-1", 1))
	require.NoError(t, err)
	withoutRule, err := Build(reloadableRule("other", 2)) // no data type 1
	require.NoError(t, err)

	r := NewReloadable(withRule)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				ids, rules := r.ResolveForDataTypes([]uint32{1})
				assert.Len(t, rules, len(ids), "torn read: rule IDs and compiled rules from different snapshots")
			}
		}()
	}

	for range 500 {
		r.Swap(withoutRule)
		r.Swap(withRule)
	}
	close(stop)
	wg.Wait()
}

// TestReloadable_ConcurrentSwap exercises concurrent readers against a
// writer that keeps publishing fresh snapshots. Run with -race.
func TestReloadable_ConcurrentSwap(t *testing.T) {
	t.Parallel()

	initial, err := Build(reloadableRule("rule-0", 1))
	require.NoError(t, err)
	r := NewReloadable(initial)

	const (
		readers = 8
		swaps   = 200
	)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				// Within a single snapshot reads are fully consistent.
				snap := r.Snapshot()
				ids := snap.GetRuleIDsByDataTypes(1)
				assert.NotEmpty(t, ids)
				assert.Len(t, snap.GetCompiledRulesByRuleIDs(ids), len(ids))

				// Across delegating calls a swap may happen in between; the
				// contract is only "no panic, unknown IDs skipped".
				r.GetCompiledRulesByRuleIDs(r.GetRuleIDsByDataTypes(1))
				r.HasRulesForDataTypes([]uint32{1})
			}
		}()
	}

	for i := 1; i <= swaps; i++ {
		reg, buildErr := Build(reloadableRule(fmt.Sprintf("rule-%d", i), 1))
		require.NoError(t, buildErr)
		r.Swap(reg)
	}
	close(stop)
	wg.Wait()
}
