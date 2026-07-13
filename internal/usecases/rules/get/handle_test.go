package get_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
	ruleerrors "github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/errors"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/get"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
)

func aRule(id string) rule.Rule {
	return rule.Rule{ID: id, Name: id, DataType: int(models.DataTypeCUSTOM)}
}

func TestHandle_Builtin(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	store := NewMockRuleStore(ctrl)
	builtins := NewMockBuiltins(ctrl)

	r := aRule("builtin_email")
	store.EXPECT().ListDisabledRuleIDs(gomock.Any()).Return(nil, nil)
	builtins.EXPECT().Get("builtin_email").Return(r, true)

	uc := get.NewUseCase(store, builtins)
	got, err := uc.Handle(context.Background(), get.Command{ID: "builtin_email"})
	require.NoError(t, err)
	assert.True(t, got.Builtin)
	assert.True(t, got.Enabled)
	assert.Equal(t, r, got.Rule)
}

func TestHandle_CustomDisabled(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	store := NewMockRuleStore(ctrl)
	builtins := NewMockBuiltins(ctrl)

	r := aRule("acme_token")
	store.EXPECT().ListDisabledRuleIDs(gomock.Any()).Return([]string{"acme_token"}, nil)
	builtins.EXPECT().Get("acme_token").Return(rule.Rule{}, false)
	store.EXPECT().GetRule(gomock.Any(), "acme_token").Return(r, nil)

	uc := get.NewUseCase(store, builtins)
	got, err := uc.Handle(context.Background(), get.Command{ID: "acme_token"})
	require.NoError(t, err)
	assert.False(t, got.Builtin)
	assert.False(t, got.Enabled)
}

func TestHandle_NotFound(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	store := NewMockRuleStore(ctrl)
	builtins := NewMockBuiltins(ctrl)

	store.EXPECT().ListDisabledRuleIDs(gomock.Any()).Return(nil, nil)
	builtins.EXPECT().Get("missing").Return(rule.Rule{}, false)
	store.EXPECT().GetRule(gomock.Any(), "missing").Return(rule.Rule{}, repository.ErrNotFound)

	uc := get.NewUseCase(store, builtins)
	_, err := uc.Handle(context.Background(), get.Command{ID: "missing"})
	require.ErrorIs(t, err, ruleerrors.ErrNotFound)
}
