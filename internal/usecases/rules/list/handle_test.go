package list_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/list"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
)

func aRule(id string) rule.Rule {
	return rule.Rule{ID: id, Name: id, DataType: int(models.DataTypeCUSTOM)}
}

func TestHandle_AllSortsCustomAndAnnotates(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	store := NewMockRuleStore(ctrl)
	builtins := NewMockBuiltins(ctrl)

	store.EXPECT().ListDisabledRuleIDs(gomock.Any()).Return([]string{"acme_a"}, nil)
	builtins.EXPECT().List().Return([]rule.Rule{aRule("builtin_email")})
	// Returned unsorted; the use case sorts custom rules by ID.
	store.EXPECT().ListRules(gomock.Any()).Return([]rule.Rule{aRule("acme_b"), aRule("acme_a")}, nil)

	uc := list.NewUseCase(store, builtins)
	got, err := uc.Handle(context.Background(), list.Command{IncludeBuiltin: true, IncludeCustom: true})
	require.NoError(t, err)
	require.Len(t, got.Rules, 3)

	assert.Equal(t, "builtin_email", got.Rules[0].Rule.ID)
	assert.True(t, got.Rules[0].Builtin)
	assert.True(t, got.Rules[0].Enabled)

	assert.Equal(t, "acme_a", got.Rules[1].Rule.ID)
	assert.False(t, got.Rules[1].Builtin)
	assert.False(t, got.Rules[1].Enabled) // in disabled set

	assert.Equal(t, "acme_b", got.Rules[2].Rule.ID)
	assert.True(t, got.Rules[2].Enabled)
}

func TestHandle_BuiltinOnly(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	store := NewMockRuleStore(ctrl)
	builtins := NewMockBuiltins(ctrl)

	store.EXPECT().ListDisabledRuleIDs(gomock.Any()).Return(nil, nil)
	builtins.EXPECT().List().Return([]rule.Rule{aRule("builtin_email")})
	// ListRules must not be called when custom is excluded.

	uc := list.NewUseCase(store, builtins)
	got, err := uc.Handle(context.Background(), list.Command{IncludeBuiltin: true})
	require.NoError(t, err)
	require.Len(t, got.Rules, 1)
	assert.True(t, got.Rules[0].Builtin)
}

func TestHandle_CustomOnly(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	store := NewMockRuleStore(ctrl)
	builtins := NewMockBuiltins(ctrl)

	store.EXPECT().ListDisabledRuleIDs(gomock.Any()).Return(nil, nil)
	// List must not be called when builtin is excluded.
	store.EXPECT().ListRules(gomock.Any()).Return([]rule.Rule{aRule("acme_token")}, nil)

	uc := list.NewUseCase(store, builtins)
	got, err := uc.Handle(context.Background(), list.Command{IncludeCustom: true})
	require.NoError(t, err)
	require.Len(t, got.Rules, 1)
	assert.False(t, got.Rules[0].Builtin)
}
