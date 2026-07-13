package settings_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository/memory"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/service/settings"
)

func newMemoryStore(t *testing.T) *memory.Store {
	t.Helper()
	s := memory.New(time.Minute, time.Minute, 0)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	return s
}

var defaults = models.GuardrailsSettings{
	Enabled:   true,
	DataTypes: []models.DataType{models.DataTypeCREDENTIALS, models.DataTypePERSONALDATA},
	Mode:      models.ModeEnforce,
}

func TestLoadSeedsDefaults(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newMemoryStore(t)

	svc := settings.New(st, defaults)
	require.NoError(t, svc.Load(ctx))

	assert.Equal(t, defaults, svc.Global())

	// The defaults must be persisted so the API reads the same source.
	stored, err := st.GetSettings(ctx)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, defaults, *stored)
}

func TestLoadPrefersStored(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newMemoryStore(t)

	// Persisted before the mode field existed: no mode in the document.
	stored := models.GuardrailsSettings{Enabled: false, DataTypes: []models.DataType{models.DataTypeAPIKEYS}}
	require.NoError(t, st.SaveSettings(ctx, stored))

	svc := settings.New(st, defaults)
	require.NoError(t, svc.Load(ctx))

	expected := stored
	expected.Mode = models.ModeEnforce // empty mode normalizes to enforce
	assert.Equal(t, expected, svc.Global())
}

func TestUpdatePersistsAndSwaps(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newMemoryStore(t)

	svc := settings.New(st, defaults)
	require.NoError(t, svc.Load(ctx))

	updated := models.GuardrailsSettings{
		Enabled:   true,
		DataTypes: []models.DataType{models.DataTypeCUSTOM},
		Mode:      models.ModeDetect,
	}
	require.NoError(t, svc.Update(ctx, updated))
	assert.Equal(t, updated, svc.Global())

	stored, err := st.GetSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, updated, *stored)
}

func TestRefreshConvergesReplicas(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newMemoryStore(t)

	a := settings.New(st, defaults)
	b := settings.New(st, defaults)
	require.NoError(t, a.Load(ctx))
	require.NoError(t, b.Load(ctx))

	updated := models.GuardrailsSettings{Enabled: false, DataTypes: nil}
	require.NoError(t, a.Update(ctx, updated))
	assert.NotEqual(t, updated.Enabled, b.Global().Enabled)

	require.NoError(t, b.Refresh(ctx))
	assert.Equal(t, updated.Enabled, b.Global().Enabled)
}
