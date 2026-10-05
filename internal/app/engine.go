package app

import (
	"context"
	"net/http"

	engineapi "github.com/cloud-ru-tech/guardrails-llm-filter/internal/controller/engine"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/logging"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
)

// defaultScanDataTypes is the scan scope used when a caller does not narrow it:
// every declared data-type group plus CUSTOM, so a bare scan exercises the whole
// ruleset. Shared by the console's /v1/scan and the engine API's /v1/mask.
func (e *App) defaultScanDataTypes() []models.DataType {
	types := make([]models.DataType, 0, len(e.DataTypes())+1)
	for _, dt := range e.DataTypes() {
		types = append(types, models.DataType(dt.DataType)) //nolint:gosec // data-type IDs are small
	}
	return append(types, models.DataTypeCUSTOM)
}

// EngineHandler returns the engine-only API handler, or nil when the listener is
// disabled (GUARDRAILS_ENGINE_API_ADDR empty).
func (e *App) EngineHandler() http.Handler {
	if e.cfg.Engine.Addr == "" {
		return nil
	}
	return engineapi.New(engineapi.Deps{
		Masker:           e.MaskUseCase(),
		Demasker:         e.DemaskerProvider(),
		Rules:            e.GuardrailsRegistry(),
		Store:            e.Store(),
		DefaultDataTypes: e.defaultScanDataTypes(),
		Token:            e.cfg.Engine.Token,
		MaxRequestBytes:  e.cfg.MaxRequestBytes,
	})
}

// EngineServer returns the engine API HTTP server, or nil when disabled. The
// masked-text state it persists reuses the configured store backend and masking
// TTL, including at-rest encryption, because it goes through the same Store.
func (e *App) EngineServer() *http.Server {
	if e.engineServer != nil {
		return e.engineServer
	}
	handler := e.EngineHandler()
	if handler == nil {
		return nil
	}
	logging.Warn(context.Background(), "Engine API is enabled: it can unmask secrets, so its bearer token must be kept secret and the listener must be reachable only by its intended caller")
	e.engineServer = &http.Server{
		Addr:              e.cfg.Engine.Addr,
		Handler:           handler,
		ReadHeaderTimeout: engineapi.ReadHeaderTimeout,
		ReadTimeout:       engineapi.ReadTimeout,
		WriteTimeout:      engineapi.WriteTimeout,
	}
	return e.engineServer
}
