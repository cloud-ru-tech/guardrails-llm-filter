package create_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/create"
	ruleerrors "github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/errors"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/validation"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
)

const ruleID = "acme_token"

func customRule() rule.Rule {
	return rule.Rule{
		ID:       ruleID,
		Name:     "Custom " + ruleID,
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

	r := customRule()
	builtins.EXPECT().IsBuiltin("acme_token").Return(false)
	store.EXPECT().CreateRule(gomock.Any(), r).Return(nil)
	store.EXPECT().SetRuleDisabled(gomock.Any(), ruleID, false).Return(nil)
	reloader.EXPECT().Reload(gomock.Any()).Return(nil)
	store.EXPECT().ListDisabledRuleIDs(gomock.Any()).Return(nil, nil)

	uc := create.NewUseCase(store, builtins, reloader, validation.Limits{}, 0)
	got, err := uc.Handle(context.Background(), create.Command{Rule: r})
	require.NoError(t, err)
	assert.Equal(t, r, got.Rule)
	assert.True(t, got.Enabled, "a freshly created rule must report enabled")
}

// A rule created over a lingering disabled flag whose clear failed must report
// its true (disabled) state, not a misleading enabled:true. Regression for #9.
func TestHandle_ClearFailed_ReportsDisabled(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	store := NewMockRuleStore(ctrl)
	builtins := NewMockBuiltins(ctrl)
	reloader := NewMockReloader(ctrl)

	r := customRule()
	builtins.EXPECT().IsBuiltin("acme_token").Return(false)
	store.EXPECT().CreateRule(gomock.Any(), r).Return(nil)
	// Clear fails (best-effort) and the flag lingers, so the reloader still
	// excludes the rule; the response must surface that.
	store.EXPECT().SetRuleDisabled(gomock.Any(), ruleID, false).Return(errors.New("clear boom"))
	reloader.EXPECT().Reload(gomock.Any()).Return(nil)
	store.EXPECT().ListDisabledRuleIDs(gomock.Any()).Return([]string{ruleID}, nil)

	uc := create.NewUseCase(store, builtins, reloader, validation.Limits{}, 0)
	got, err := uc.Handle(context.Background(), create.Command{Rule: r})
	require.NoError(t, err)
	assert.False(t, got.Enabled, "response must reflect the rule is still disabled")
}

func TestHandle_ValidationError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	// No store/builtins/reloader calls expected: validation fails first.
	store := NewMockRuleStore(ctrl)
	builtins := NewMockBuiltins(ctrl)
	reloader := NewMockReloader(ctrl)

	bad := customRule()
	bad.Regex = "(unclosed"

	uc := create.NewUseCase(store, builtins, reloader, validation.Limits{}, 0)
	_, err := uc.Handle(context.Background(), create.Command{Rule: bad})
	var verr *ruleerrors.ValidationError
	require.ErrorAs(t, err, &verr)
}

func TestHandle_BuiltinConflict(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	store := NewMockRuleStore(ctrl)
	builtins := NewMockBuiltins(ctrl)
	reloader := NewMockReloader(ctrl)

	builtins.EXPECT().IsBuiltin("acme_token").Return(true)

	uc := create.NewUseCase(store, builtins, reloader, validation.Limits{}, 0)
	_, err := uc.Handle(context.Background(), create.Command{Rule: customRule()})
	require.ErrorIs(t, err, ruleerrors.ErrAlreadyExists)
}

func TestHandle_CustomConflict(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	store := NewMockRuleStore(ctrl)
	builtins := NewMockBuiltins(ctrl)
	reloader := NewMockReloader(ctrl)

	builtins.EXPECT().IsBuiltin("acme_token").Return(false)
	store.EXPECT().CreateRule(gomock.Any(), gomock.Any()).Return(repository.ErrAlreadyExists)

	uc := create.NewUseCase(store, builtins, reloader, validation.Limits{}, 0)
	_, err := uc.Handle(context.Background(), create.Command{Rule: customRule()})
	require.ErrorIs(t, err, ruleerrors.ErrAlreadyExists)
}

func TestHandle_StoreError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	store := NewMockRuleStore(ctrl)
	builtins := NewMockBuiltins(ctrl)
	reloader := NewMockReloader(ctrl)

	boom := errors.New("boom")
	builtins.EXPECT().IsBuiltin("acme_token").Return(false)
	store.EXPECT().CreateRule(gomock.Any(), gomock.Any()).Return(boom)

	uc := create.NewUseCase(store, builtins, reloader, validation.Limits{}, 0)
	_, err := uc.Handle(context.Background(), create.Command{Rule: customRule()})
	require.ErrorIs(t, err, boom)
	assert.NotErrorIs(t, err, ruleerrors.ErrAlreadyExists)
}

func TestHandle_ReloadError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	store := NewMockRuleStore(ctrl)
	builtins := NewMockBuiltins(ctrl)
	reloader := NewMockReloader(ctrl)

	boom := errors.New("reload boom")
	builtins.EXPECT().IsBuiltin("acme_token").Return(false)
	store.EXPECT().CreateRule(gomock.Any(), gomock.Any()).Return(nil)
	store.EXPECT().SetRuleDisabled(gomock.Any(), ruleID, false).Return(nil)
	reloader.EXPECT().Reload(gomock.Any()).Return(boom)

	uc := create.NewUseCase(store, builtins, reloader, validation.Limits{}, 0)
	_, err := uc.Handle(context.Background(), create.Command{Rule: customRule()})
	require.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "reload failed")
}

func TestHandle_PatternTooLong(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	// No store/builtins/reloader calls expected: validation fails first.
	store := NewMockRuleStore(ctrl)
	builtins := NewMockBuiltins(ctrl)
	reloader := NewMockReloader(ctrl)

	long := customRule()
	long.Regex = `\b` + strings.Repeat("a", 100) + `\b`

	uc := create.NewUseCase(store, builtins, reloader, validation.Limits{MaxPatternLen: 16}, 0)
	_, err := uc.Handle(context.Background(), create.Command{Rule: long})
	var verr *ruleerrors.ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Contains(t, err.Error(), "regex too long")
}

func TestHandle_TooManyRules(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	store := NewMockRuleStore(ctrl)
	builtins := NewMockBuiltins(ctrl)
	reloader := NewMockReloader(ctrl)

	builtins.EXPECT().IsBuiltin("acme_token").Return(false)
	// Already at the limit of 2 custom rules: the create must be rejected
	// before any write and without a registry reload.
	store.EXPECT().ListRules(gomock.Any()).Return([]rule.Rule{{ID: "a"}, {ID: "b"}}, nil)

	uc := create.NewUseCase(store, builtins, reloader, validation.Limits{}, 2)
	_, err := uc.Handle(context.Background(), create.Command{Rule: customRule()})
	require.ErrorIs(t, err, ruleerrors.ErrTooManyRules)
}
