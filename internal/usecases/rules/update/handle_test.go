package update_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
	ruleerrors "github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/errors"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/update"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/validation"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
)

func customRule(id string) rule.Rule {
	return rule.Rule{
		ID:       id,
		Name:     "Custom " + id,
		DataType: int(models.DataTypeCUSTOM),
		Regex:    `\bacme-[0-9a-f]{8}\b`,
		Masking:  rule.MaskingConfig{Placeholder: "ACME_TOKEN"},
	}
}

func TestHandle_Success(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	store := NewMockRuleStore(ctrl)
	builtins := NewMockBuiltins(ctrl)
	reloader := NewMockReloader(ctrl)

	r := customRule("acme_token")
	builtins.EXPECT().IsBuiltin("acme_token").Return(false)
	store.EXPECT().GetRule(gomock.Any(), "acme_token").Return(r, nil)
	store.EXPECT().SaveRule(gomock.Any(), r).Return(nil)
	reloader.EXPECT().Reload(gomock.Any()).Return(nil)
	store.EXPECT().ListDisabledRuleIDs(gomock.Any()).Return(nil, nil)

	uc := update.NewUseCase(store, builtins, reloader, validation.Limits{})
	got, err := uc.Handle(context.Background(), update.Command{Rule: r})
	require.NoError(t, err)
	assert.Equal(t, r, got.Rule)
	assert.True(t, got.Enabled)
}

func TestHandle_ReflectsDisabledState(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	store := NewMockRuleStore(ctrl)
	builtins := NewMockBuiltins(ctrl)
	reloader := NewMockReloader(ctrl)

	r := customRule("acme_token")
	builtins.EXPECT().IsBuiltin("acme_token").Return(false)
	store.EXPECT().GetRule(gomock.Any(), "acme_token").Return(r, nil)
	store.EXPECT().SaveRule(gomock.Any(), r).Return(nil)
	reloader.EXPECT().Reload(gomock.Any()).Return(nil)
	store.EXPECT().ListDisabledRuleIDs(gomock.Any()).Return([]string{"acme_token"}, nil)

	uc := update.NewUseCase(store, builtins, reloader, validation.Limits{})
	got, err := uc.Handle(context.Background(), update.Command{Rule: r})
	require.NoError(t, err)
	assert.False(t, got.Enabled)
}

func TestHandle_Builtin(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	store := NewMockRuleStore(ctrl)
	builtins := NewMockBuiltins(ctrl)
	reloader := NewMockReloader(ctrl)

	builtins.EXPECT().IsBuiltin("builtin_email").Return(true)

	uc := update.NewUseCase(store, builtins, reloader, validation.Limits{})
	_, err := uc.Handle(context.Background(), update.Command{Rule: customRule("builtin_email")})
	require.ErrorIs(t, err, ruleerrors.ErrBuiltin)
}

func TestHandle_NotFound(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	store := NewMockRuleStore(ctrl)
	builtins := NewMockBuiltins(ctrl)
	reloader := NewMockReloader(ctrl)

	builtins.EXPECT().IsBuiltin("missing").Return(false)
	store.EXPECT().GetRule(gomock.Any(), "missing").Return(rule.Rule{}, repository.ErrNotFound)

	uc := update.NewUseCase(store, builtins, reloader, validation.Limits{})
	_, err := uc.Handle(context.Background(), update.Command{Rule: customRule("missing")})
	require.ErrorIs(t, err, ruleerrors.ErrNotFound)
}

func TestHandle_ValidationError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	store := NewMockRuleStore(ctrl)
	builtins := NewMockBuiltins(ctrl)
	reloader := NewMockReloader(ctrl)

	bad := customRule("acme_token")
	bad.Regex = "(unclosed"
	builtins.EXPECT().IsBuiltin("acme_token").Return(false)

	uc := update.NewUseCase(store, builtins, reloader, validation.Limits{})
	_, err := uc.Handle(context.Background(), update.Command{Rule: bad})
	var verr *ruleerrors.ValidationError
	require.ErrorAs(t, err, &verr)
}
