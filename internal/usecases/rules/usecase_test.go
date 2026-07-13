package rules_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository/memory"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/service/rulesreload"
	rulesuc "github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/builtins"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/create"
	deleterule "github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/delete"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/list"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/setenabled"
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

// newCoordinator wires the rules coordinator over a real store + reloader the
// same way production does, so tests can drive scenarios and inspect the
// resulting registry snapshot.
func newCoordinator(t *testing.T) (*rulesuc.UseCase, *registry.Reloadable) {
	t.Helper()
	st := memory.New(time.Minute, time.Minute, 0)
	t.Cleanup(func() { require.NoError(t, st.Close()) })

	builtin := []rule.Rule{builtinRule()}
	initial, err := registry.Build(builtin...)
	require.NoError(t, err)
	reg := registry.NewReloadable(initial)
	reloader := rulesreload.New(builtin, st, reg)

	uc := rulesuc.NewUseCase(rulesuc.Deps{
		Store:    st,
		Builtins: builtins.New(builtin),
		Reloader: reloader,
	})
	return uc, reg
}

func TestCreateAppearsInRegistry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	uc, reg := newCoordinator(t)

	_, err := uc.Create().Handle(ctx, create.Command{Rule: customRule("acme_token")})
	require.NoError(t, err)

	assert.Equal(t, []string{"acme_token"}, reg.GetRuleIDsByDataTypes(uint32(models.DataTypeCUSTOM)))
	// Built-in rules survive the reload.
	assert.Equal(t, []string{"builtin_email"}, reg.GetRuleIDsByDataTypes(uint32(models.DataTypePERSONALDATA)))
}

func TestConcurrentCreates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	uc, reg := newCoordinator(t)

	const n = 20
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = uc.Create().Handle(ctx, create.Command{Rule: customRule(fmt.Sprintf("rule-%03d", i))})
		}()
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "create %d", i)
	}

	custom, err := uc.List().Handle(ctx, list.Command{IncludeCustom: true})
	require.NoError(t, err)
	assert.Len(t, custom.Rules, n)
	assert.Len(t, reg.GetRuleIDsByDataTypes(uint32(models.DataTypeCUSTOM)), n)
}

func TestSetEnabledDisablesBuiltinInRegistry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	uc, reg := newCoordinator(t)

	_, err := uc.SetEnabled().Handle(ctx, setenabled.Command{ID: "builtin_email", Enabled: false})
	require.NoError(t, err)
	assert.Empty(t, reg.GetRuleIDsByDataTypes(uint32(models.DataTypePERSONALDATA)),
		"disabled built-in rule must be excluded from the compiled snapshot")

	_, err = uc.SetEnabled().Handle(ctx, setenabled.Command{ID: "builtin_email", Enabled: true})
	require.NoError(t, err)
	assert.Equal(t, []string{"builtin_email"}, reg.GetRuleIDsByDataTypes(uint32(models.DataTypePERSONALDATA)))
}

func TestDeleteClearsDisabledFlag(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	uc, reg := newCoordinator(t)

	_, err := uc.Create().Handle(ctx, create.Command{Rule: customRule("acme_token")})
	require.NoError(t, err)
	_, err = uc.SetEnabled().Handle(ctx, setenabled.Command{ID: "acme_token", Enabled: false})
	require.NoError(t, err)
	_, err = uc.Delete().Handle(ctx, deleterule.Command{ID: "acme_token"})
	require.NoError(t, err)

	// Re-creating the same ID must yield an active rule.
	_, err = uc.Create().Handle(ctx, create.Command{Rule: customRule("acme_token")})
	require.NoError(t, err)
	assert.Equal(t, []string{"acme_token"}, reg.GetRuleIDsByDataTypes(uint32(models.DataTypeCUSTOM)))
}
