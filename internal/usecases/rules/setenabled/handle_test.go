package setenabled_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
	ruleerrors "github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/errors"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/setenabled"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
)

func aRule(id string) rule.Rule {
	return rule.Rule{ID: id, Name: id, DataType: int(models.DataTypeCUSTOM)}
}

func TestHandle_DisableBuiltin(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	store := NewMockRuleStore(ctrl)
	builtins := NewMockBuiltins(ctrl)
	reloader := NewMockReloader(ctrl)

	r := aRule("builtin_email")
	builtins.EXPECT().IsBuiltin("builtin_email").Return(true)
	store.EXPECT().SetRuleDisabled(gomock.Any(), "builtin_email", true).Return(nil)
	reloader.EXPECT().Reload(gomock.Any()).Return(nil)
	builtins.EXPECT().Get("builtin_email").Return(r, true)

	uc := setenabled.NewUseCase(store, builtins, reloader)
	got, err := uc.Handle(context.Background(), setenabled.Command{ID: "builtin_email", Enabled: false})
	require.NoError(t, err)
	assert.True(t, got.Builtin)
	assert.False(t, got.Enabled)
	assert.Equal(t, r, got.Rule)
}

func TestHandle_DisableCustom(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	store := NewMockRuleStore(ctrl)
	builtins := NewMockBuiltins(ctrl)
	reloader := NewMockReloader(ctrl)

	r := aRule("acme_token")
	builtins.EXPECT().IsBuiltin("acme_token").Return(false)
	// GetRule is called for the existence check and again to resolve the view.
	store.EXPECT().GetRule(gomock.Any(), "acme_token").Return(r, nil).Times(2)
	store.EXPECT().SetRuleDisabled(gomock.Any(), "acme_token", true).Return(nil)
	reloader.EXPECT().Reload(gomock.Any()).Return(nil)

	uc := setenabled.NewUseCase(store, builtins, reloader)
	got, err := uc.Handle(context.Background(), setenabled.Command{ID: "acme_token", Enabled: false})
	require.NoError(t, err)
	assert.False(t, got.Builtin)
	assert.False(t, got.Enabled)
	assert.Equal(t, r, got.Rule)
}

func TestHandle_EnableCustom(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	store := NewMockRuleStore(ctrl)
	builtins := NewMockBuiltins(ctrl)
	reloader := NewMockReloader(ctrl)

	r := aRule("acme_token")
	builtins.EXPECT().IsBuiltin("acme_token").Return(false)
	store.EXPECT().GetRule(gomock.Any(), "acme_token").Return(r, nil).Times(2)
	// enabled=true → disabled flag cleared (false).
	store.EXPECT().SetRuleDisabled(gomock.Any(), "acme_token", false).Return(nil)
	reloader.EXPECT().Reload(gomock.Any()).Return(nil)

	uc := setenabled.NewUseCase(store, builtins, reloader)
	got, err := uc.Handle(context.Background(), setenabled.Command{ID: "acme_token", Enabled: true})
	require.NoError(t, err)
	assert.True(t, got.Enabled)
}

func TestHandle_CustomNotFound(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	store := NewMockRuleStore(ctrl)
	builtins := NewMockBuiltins(ctrl)
	reloader := NewMockReloader(ctrl)

	builtins.EXPECT().IsBuiltin("missing").Return(false)
	store.EXPECT().GetRule(gomock.Any(), "missing").Return(rule.Rule{}, repository.ErrNotFound)

	uc := setenabled.NewUseCase(store, builtins, reloader)
	_, err := uc.Handle(context.Background(), setenabled.Command{ID: "missing", Enabled: false})
	require.ErrorIs(t, err, ruleerrors.ErrNotFound)
}
