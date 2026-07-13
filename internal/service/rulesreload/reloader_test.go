package rulesreload_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository/memory"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/service/rulesreload"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/registry"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
)

func builtinRule() rule.Rule {
	return rule.Rule{
		ID:       "builtin_email",
		Name:     "Email",
		DataType: int(models.DataTypePERSONALDATA),
		Regex:    `[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}`,
		Masking:  rule.MaskingConfig{Placeholder: "EMAIL"},
	}
}

func customRule(id string) rule.Rule {
	return rule.Rule{
		ID:       id,
		Name:     "Custom " + id,
		DataType: int(models.DataTypeCUSTOM),
		Regex:    `\bacme-[0-9a-f]{8}\b`,
		Masking:  rule.MaskingConfig{Placeholder: "ACME_TOKEN"},
	}
}

func newReloader(t *testing.T) (*rulesreload.Reloader, *memory.Store) {
	t.Helper()
	st := memory.New(time.Minute, time.Minute, 0)
	t.Cleanup(func() { require.NoError(t, st.Close()) })

	builtin := []rule.Rule{builtinRule()}
	initial, err := registry.Build(builtin...)
	require.NoError(t, err)

	return rulesreload.New(builtin, st, registry.NewReloadable(initial)), st
}

func TestReloadSkipsShadowingStoredRule(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rel, st := newReloader(t)

	// A rule that shadows a built-in ID sneaks into the store directly
	// (e.g. written by an older replica).
	require.NoError(t, st.SaveRule(ctx, customRule("builtin_email")))
	require.NoError(t, rel.Reload(ctx))

	// The built-in rule wins; the registry still compiles.
	got := rel.Registry().GetRulesByIDs("builtin_email")
	require.Len(t, got, 1)
	assert.Equal(t, int(models.DataTypePERSONALDATA), got[0].DataType)
}

func TestReloadConvergesOnDisabledSet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rel, st := newReloader(t)

	// Another replica disabled a built-in rule and left a stale ID behind.
	require.NoError(t, st.SetRuleDisabled(ctx, "builtin_email", true))
	require.NoError(t, st.SetRuleDisabled(ctx, "ghost.rule", true))

	require.NoError(t, rel.Reload(ctx))
	assert.Empty(t, rel.Registry().GetRuleIDsByDataTypes(uint32(models.DataTypePERSONALDATA)))
}

// failingDisabledStore wraps the memory store and fails ListDisabledRuleIDs.
type failingDisabledStore struct {
	*memory.Store
	fail bool
}

func (s *failingDisabledStore) ListDisabledRuleIDs(ctx context.Context) ([]string, error) {
	if s.fail {
		return nil, errors.New("boom")
	}
	return s.Store.ListDisabledRuleIDs(ctx)
}

func TestReloadDisabledListErrorKeepsSnapshot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	st := memory.New(time.Minute, time.Minute, 0)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	wrapped := &failingDisabledStore{Store: st}

	builtin := []rule.Rule{builtinRule()}
	initial, err := registry.Build(builtin...)
	require.NoError(t, err)
	rel := rulesreload.New(builtin, wrapped, registry.NewReloadable(initial))

	wrapped.fail = true
	require.Error(t, rel.Reload(ctx))

	// The previous snapshot keeps serving (fail-open).
	assert.Equal(t, []string{"builtin_email"},
		rel.Registry().GetRuleIDsByDataTypes(uint32(models.DataTypePERSONALDATA)))
}
