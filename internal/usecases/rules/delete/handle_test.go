package delete_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
	deleterule "github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/delete"
	ruleerrors "github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/errors"
)

func TestHandle_Success(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	store := NewMockRuleStore(ctrl)
	builtins := NewMockBuiltins(ctrl)
	reloader := NewMockReloader(ctrl)

	builtins.EXPECT().IsBuiltin("acme_token").Return(false)
	store.EXPECT().DeleteRule(gomock.Any(), "acme_token").Return(nil)
	store.EXPECT().SetRuleDisabled(gomock.Any(), "acme_token", false).Return(nil)
	reloader.EXPECT().Reload(gomock.Any()).Return(nil)

	uc := deleterule.NewUseCase(store, builtins, reloader)
	_, err := uc.Handle(context.Background(), deleterule.Command{ID: "acme_token"})
	require.NoError(t, err)
}

func TestHandle_Builtin(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	store := NewMockRuleStore(ctrl)
	builtins := NewMockBuiltins(ctrl)
	reloader := NewMockReloader(ctrl)

	builtins.EXPECT().IsBuiltin("builtin_email").Return(true)

	uc := deleterule.NewUseCase(store, builtins, reloader)
	_, err := uc.Handle(context.Background(), deleterule.Command{ID: "builtin_email"})
	require.ErrorIs(t, err, ruleerrors.ErrBuiltin)
}

func TestHandle_NotFound(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	store := NewMockRuleStore(ctrl)
	builtins := NewMockBuiltins(ctrl)
	reloader := NewMockReloader(ctrl)

	builtins.EXPECT().IsBuiltin("missing").Return(false)
	store.EXPECT().DeleteRule(gomock.Any(), "missing").Return(repository.ErrNotFound)

	uc := deleterule.NewUseCase(store, builtins, reloader)
	_, err := uc.Handle(context.Background(), deleterule.Command{ID: "missing"})
	require.ErrorIs(t, err, ruleerrors.ErrNotFound)
}

// A failure to clear the disabled flag is best-effort and must not fail the
// delete.
func TestHandle_ClearDisabledFlagBestEffort(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	store := NewMockRuleStore(ctrl)
	builtins := NewMockBuiltins(ctrl)
	reloader := NewMockReloader(ctrl)

	builtins.EXPECT().IsBuiltin("acme_token").Return(false)
	store.EXPECT().DeleteRule(gomock.Any(), "acme_token").Return(nil)
	store.EXPECT().SetRuleDisabled(gomock.Any(), "acme_token", false).Return(errors.New("boom"))
	reloader.EXPECT().Reload(gomock.Any()).Return(nil)

	uc := deleterule.NewUseCase(store, builtins, reloader)
	_, err := uc.Handle(context.Background(), deleterule.Command{ID: "acme_token"})
	require.NoError(t, err)
}

func TestHandle_ReloadError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	store := NewMockRuleStore(ctrl)
	builtins := NewMockBuiltins(ctrl)
	reloader := NewMockReloader(ctrl)

	boom := errors.New("reload boom")
	builtins.EXPECT().IsBuiltin("acme_token").Return(false)
	store.EXPECT().DeleteRule(gomock.Any(), "acme_token").Return(nil)
	store.EXPECT().SetRuleDisabled(gomock.Any(), "acme_token", false).Return(nil)
	reloader.EXPECT().Reload(gomock.Any()).Return(boom)

	uc := deleterule.NewUseCase(store, builtins, reloader)
	_, err := uc.Handle(context.Background(), deleterule.Command{ID: "acme_token"})
	require.ErrorIs(t, err, boom)
}
