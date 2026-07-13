package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/multierr"
	"google.golang.org/grpc"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/config"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/controller/api"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/controller/gateway"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/guardrails/demask"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/health"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/logging"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
	storefactory "github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository/factory"
	storeredis "github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository/redis"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository/statecodec"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/service/audit"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/service/rulesreload"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/service/settings"
	maskuc "github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/guardrails/mask"
	rulesuc "github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/builtins"
	gregistry "github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/registry"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/scanners/placeholder"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/scanners/sensitive"
)

// App is the standalone guardrails service: a lazy-getter dependency container
// that builds and runs the data-plane gateway, the Prometheus metrics server
// and the configuration API. Getters panic on boot-time misconfiguration by
// design — a misconfigured pod must fail fast rather than serve degraded.
type App struct {
	cfg *config.Config

	metricsServer   *http.Server
	gatewayServer   *http.Server
	apiServer       *http.Server
	grpcServer      *grpc.Server
	grpcController  *api.Controller
	metricsGatherer prometheus.Gatherer
	gateway         *gateway.Handler
	stop            func() error

	maskUC *maskuc.UseCase

	demaskerProvider *demask.Provider

	store              repository.Store
	codec              statecodec.Codec
	settingsService    *settings.Service
	rulesUC            *rulesuc.UseCase
	builtinsIndex      *builtins.Index
	rulesReloader      *rulesreload.Reloader
	auditRecorder      *audit.Recorder
	dataTypes          []rule.DataType
	fileRules          []rule.Rule
	guardrailsRegistry *gregistry.Reloadable
	sensitiveScanner   *sensitive.Scanner
	placeholderScanner *placeholder.Scanner
}

// New creates a new App instance.
func New(cfg *config.Config) *App {
	return &App{cfg: cfg, metricsGatherer: prometheus.DefaultGatherer}
}

// Store returns the configured persistence backend (masking state, custom
// rules, global settings). Panics on misconfiguration or unreachable
// external backend: this is a boot-time error.
func (e *App) Store() repository.Store {
	if e.store != nil {
		return e.store
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	st, err := storefactory.New(ctx, storefactory.Config{
		Backend:         storefactory.Backend(e.cfg.Store.Backend),
		MaskingTTL:      e.cfg.Store.MaskingTTL,
		AuditTTL:        e.cfg.Audit.Retention,
		AuditMaxEntries: e.cfg.Audit.MaxEntries,
		Redis: storeredis.Config{
			Addr:     e.cfg.Store.Redis.Addr,
			Password: e.cfg.Store.Redis.Password,
			DB:       e.cfg.Store.Redis.DB,
		},
		PostgresDSN:       e.cfg.Store.PostgresDSN,
		EncryptionEnabled: e.cfg.Store.EncryptionEnabled,
		EncryptionKey:     e.cfg.Store.EncryptionKey,
	})
	if err != nil {
		panic(fmt.Errorf("create store (backend %q): %w", e.cfg.Store.Backend, err))
	}
	e.store = st
	return e.store
}

// storeCodec returns the statecodec used for at-rest encryption, matching the
// store's own configuration. It is shared with the audit recorder (to seal
// originals) and the API controller (to open them). Same key as the store
// factory builds internally, so envelopes are mutually decryptable. Panics on
// a misconfigured key: a boot-time error (config.Load already rejects
// encrypted originals without a key, so this is defense in depth).
func (e *App) storeCodec() statecodec.Codec {
	if e.codec != nil {
		return e.codec
	}
	if e.cfg.Store.EncryptionEnabled {
		c, err := statecodec.NewAESGCMFromBase64(e.cfg.Store.EncryptionKey)
		if err != nil {
			panic(fmt.Errorf("build store codec: %w", err))
		}
		e.codec = c
	} else {
		e.codec = statecodec.Plain()
	}
	return e.codec
}

// SettingsService returns the global guardrails settings service.
func (e *App) SettingsService() *settings.Service {
	if e.settingsService != nil {
		return e.settingsService
	}

	// An empty GUARDRAILS_DATA_TYPES seeds an empty enabled-types set (masking
	// effectively off until configured via the API) rather than crashing the
	// process. ParseDataTypes errors on empty input, so guard it here.
	var dataTypes []models.DataType
	if strings.TrimSpace(e.cfg.Guardrails.DataTypes) != "" {
		parsed, err := settings.ParseDataTypes(e.cfg.Guardrails.DataTypes)
		if err != nil {
			panic(fmt.Errorf("parse GUARDRAILS_DATA_TYPES %q: %w", e.cfg.Guardrails.DataTypes, err))
		}
		dataTypes = parsed
	}
	mode, err := models.ParseGuardrailsMode(e.cfg.Guardrails.Mode)
	if err != nil {
		panic(fmt.Errorf("parse GUARDRAILS_MODE: %w", err))
	}

	e.settingsService = settings.New(e.Store(), models.GuardrailsSettings{
		Enabled:   e.cfg.Guardrails.Enabled,
		DataTypes: dataTypes,
		Mode:      mode,
	})
	return e.settingsService
}

// RulesUseCase returns the rule-management use-case coordinator (custom-rule
// CRUD, enable/disable, read/list) backing the configuration API.
func (e *App) RulesUseCase() *rulesuc.UseCase {
	if e.rulesUC != nil {
		return e.rulesUC
	}
	e.rulesUC = rulesuc.NewUseCase(rulesuc.Deps{
		Store:          e.Store(),
		Builtins:       e.BuiltinsIndex(),
		Reloader:       e.RulesReloader(),
		MaxCustomRules: e.cfg.GuardrailsRules.MaxCustom,
		MaxPatternLen:  e.cfg.GuardrailsRules.MaxPatternLen,
	})
	return e.rulesUC
}

// BuiltinsIndex returns the immutable index over the built-in rules loaded
// from the YAML files, shared by the rule use cases.
func (e *App) BuiltinsIndex() *builtins.Index {
	if e.builtinsIndex != nil {
		return e.builtinsIndex
	}
	e.builtinsIndex = builtins.New(e.loadFileRules())
	return e.builtinsIndex
}

// RulesReloader returns the registry merge-and-swap service shared by the
// boot-time load, the refresh ticker and the rule-mutation use cases.
func (e *App) RulesReloader() *rulesreload.Reloader {
	if e.rulesReloader != nil {
		return e.rulesReloader
	}
	e.rulesReloader = rulesreload.New(e.loadFileRules(), e.Store(), e.GuardrailsRegistry())
	return e.rulesReloader
}

// DataTypes returns the data-type groups declared in the rule files.
func (e *App) DataTypes() []rule.DataType {
	e.loadFileRules()
	return e.dataTypes
}

func (e *App) loadFileRules() []rule.Rule {
	if e.fileRules != nil {
		return e.fileRules
	}
	dataTypes, rules, err := rule.LoadAllFromFiles(
		e.cfg.GuardrailsRules.RegexRulesFile,
		e.cfg.GuardrailsRules.GitleaksRegexRulesFile,
	)
	if err != nil {
		panic(fmt.Errorf(
			"load guardrails rules from %s and %s: %w",
			e.cfg.GuardrailsRules.RegexRulesFile,
			e.cfg.GuardrailsRules.GitleaksRegexRulesFile,
			err,
		))
	}
	e.dataTypes = dataTypes
	e.fileRules = rules
	return e.fileRules
}

// GuardrailsRegistry returns the reloadable compiled-rule registry seeded
// with the file rules. Custom rules from the store are merged in by
// RulesReloader().Reload during Start.
func (e *App) GuardrailsRegistry() *gregistry.Reloadable {
	if e.guardrailsRegistry != nil {
		return e.guardrailsRegistry
	}
	reg := gregistry.NewRegistry()
	reg.Register(e.loadFileRules()...)
	e.guardrailsRegistry = gregistry.NewReloadable(reg)
	return e.guardrailsRegistry
}

func (e *App) SensitiveScanner() *sensitive.Scanner {
	if e.sensitiveScanner != nil {
		return e.sensitiveScanner
	}
	if e.cfg.Guardrails.KeywordPrefilterEnabled {
		// Surface which keyword-bearing rules are NOT pre-filtered (their regex
		// does not guarantee a keyword in every match) so operators can see what
		// stays fully scanned. Only rule IDs are logged — not sensitive.
		if ineligible := e.GuardrailsRegistry().PrefilterIneligibleRuleIDs(); len(ineligible) > 0 {
			logging.Info(context.Background(),
				"keyword pre-filter enabled; these rules declare keywords but are always scanned (regex does not guarantee a keyword in every match)",
				"count", len(ineligible),
				"rule_ids", ineligible,
			)
		}
	}
	e.sensitiveScanner = sensitive.New(
		e.GuardrailsRegistry(),
		sensitive.WithKeywordPrefilter(e.cfg.Guardrails.KeywordPrefilterEnabled),
	)
	return e.sensitiveScanner
}

func (e *App) PlaceholderScanner() *placeholder.Scanner {
	if e.placeholderScanner != nil {
		return e.placeholderScanner
	}
	e.placeholderScanner = placeholder.New(e.GuardrailsRegistry())
	return e.placeholderScanner
}

func (e *App) DemaskerProvider() *demask.Provider {
	if e.demaskerProvider != nil {
		return e.demaskerProvider
	}
	e.demaskerProvider = demask.NewProvider(
		e.GuardrailsRegistry(),
		e.PlaceholderScanner(),
	)
	return e.demaskerProvider
}

// MaskUseCase returns the mask use case.
func (e *App) MaskUseCase() *maskuc.UseCase {
	if e.maskUC != nil {
		return e.maskUC
	}
	e.maskUC = maskuc.New(maskuc.Deps{
		Registry: e.GuardrailsRegistry(),
		Scanner:  e.SensitiveScanner(),
	}, maskuc.WithParallelMinBytes(e.cfg.Guardrails.MaskParallelMinBytes))
	return e.maskUC
}

// AuditRecorder returns the masking audit recorder, or nil when the audit
// trail is disabled.
func (e *App) AuditRecorder() *audit.Recorder {
	if e.auditRecorder != nil {
		return e.auditRecorder
	}
	if !e.cfg.Audit.Enabled {
		return nil
	}
	e.auditRecorder = audit.New(e.Store(), e.GuardrailsRegistry(),
		e.cfg.Audit.StoreMaskedTexts, e.cfg.Audit.StoreMaskedResponseTexts,
		e.cfg.Audit.StoreOriginalTexts, e.storeCodec())
	return e.auditRecorder
}

// Gateway returns the data-plane HTTP handler (mask → forward → demask).
func (e *App) Gateway() *gateway.Handler {
	if e.gateway != nil {
		return e.gateway
	}
	// The interface value must be a nil literal when audit is disabled — a
	// typed-nil *audit.Recorder would defeat the gateway's nil check.
	var recorder gateway.AuditRecorder
	if r := e.AuditRecorder(); r != nil {
		recorder = r
	}
	gw, err := gateway.New(
		e.cfg,
		e.MaskUseCase(),
		e.SettingsService(),
		e.DemaskerProvider(),
		recorder,
	)
	if err != nil {
		panic(fmt.Errorf("create gateway handler: %w", err))
	}
	if e.cfg.Upstream.BaseURL == "" && len(e.cfg.Upstream.PathBaseURLs) == 0 {
		panic(fmt.Errorf("no upstream configured: set GUARDRAILS_UPSTREAM_BASE_URL (or GUARDRAILS_UPSTREAM_PATH_BASE_URLS)"))
	}
	e.gateway = gw
	return e.gateway
}

// GatewayServer returns the data-plane HTTP server. It mounts the gateway
// handler at "/" and reserves "/healthz" (liveness) and "/readyz" (readiness)
// for probes; those two paths are never proxied upstream.
func (e *App) GatewayServer() *http.Server {
	if e.gatewayServer != nil {
		return e.gatewayServer
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", livenessHandler)
	mux.HandleFunc("GET /readyz", readinessHandler)
	mux.Handle("/", e.Gateway())
	e.gatewayServer = &http.Server{
		Addr:              e.cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return e.gatewayServer
}

// MetricsServer returns the Prometheus metrics HTTP server.
func (e *App) MetricsServer() *http.Server {
	if e.metricsServer != nil {
		return e.metricsServer
	}
	e.metricsServer = &http.Server{
		Addr:              fmt.Sprintf(":%d", e.cfg.MetricsPort),
		Handler:           promhttp.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return e.metricsServer
}

// Start brings up all servers and registers a graceful shutdown handler.
func (e *App) Start(ctx context.Context) error {
	// Initialise settings and merge stored custom rules before serving.
	// Both are fail-open: env defaults / file rules serve until the store
	// heals via the refresh tickers.
	if err := e.SettingsService().Load(ctx); err != nil {
		logging.Error(ctx, "Failed to load settings from store, using env defaults", err)
	}
	if err := e.RulesReloader().Reload(ctx); err != nil {
		logging.Error(ctx, "Failed to load custom rules from store, serving file rules only", err)
	}

	if e.cfg.Audit.Enabled {
		logging.Info(ctx, "Masking audit trail enabled",
			"store_masked_texts", e.cfg.Audit.StoreMaskedTexts,
			"store_masked_response_texts", e.cfg.Audit.StoreMaskedResponseTexts,
			"store_original_texts", e.cfg.Audit.StoreOriginalTexts,
			"retention", e.cfg.Audit.Retention.String())
	}

	gatewaySrv := e.GatewayServer()
	go func() {
		logging.Info(ctx, "Starting gateway server", "addr", gatewaySrv.Addr, "upstream", e.cfg.Upstream.BaseURL)
		if err := gatewaySrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logging.Error(ctx, "gateway server error", err)
		}
	}()

	go func() {
		logging.Info(ctx, "Starting metrics server", "port", e.cfg.MetricsPort)
		if err := e.MetricsServer().ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logging.Error(ctx, "metrics server error", err)
		}
	}()

	// Management gRPC server. The REST gateway (APIServer) proxies to it, so it
	// runs whenever the service is up, independent of the REST listen address.
	grpcLis, err := net.Listen("tcp", e.cfg.GRPCAddr)
	if err != nil {
		return fmt.Errorf("listen gRPC on %s: %w", e.cfg.GRPCAddr, err)
	}
	grpcSrv := e.GrpcServer(ctx)
	go func() {
		logging.Info(ctx, "Starting management gRPC server", "addr", e.cfg.GRPCAddr, "secure", e.cfg.GrpcSecure)
		if err := grpcSrv.Serve(grpcLis); err != nil && err != grpc.ErrServerStopped {
			logging.Error(ctx, "management gRPC server error", err)
		}
	}()

	if apiSrv := e.APIServer(ctx); apiSrv != nil {
		go func() {
			logging.Info(ctx, "Starting management REST server (grpc-gateway)", "addr", apiSrv.Addr)
			if err := apiSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logging.Error(ctx, "management REST server error", err)
			}
		}()
	}

	// Background refresh converges replicas on API changes when the store
	// backend is shared.
	refreshCtx, cancelRefresh := context.WithCancel(context.Background())
	go e.SettingsService().RunRefresh(refreshCtx, e.cfg.Guardrails.SettingsRefreshInterval)
	go e.RulesReloader().RunRefresh(refreshCtx, e.cfg.Guardrails.RulesRefreshInterval)

	e.stop = func() error {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		cancelRefresh()

		// Shut the data plane first so no new masking work starts, then the
		// ops/control servers. Shutdown drains in-flight requests (bounded by
		// shutdownCtx), including long-lived SSE responses.
		gatewayErr := e.GatewayServer().Shutdown(shutdownCtx)

		var apiErr error
		if e.apiServer != nil {
			apiErr = e.apiServer.Shutdown(shutdownCtx)
		}

		// Gracefully stop the management gRPC server, bounded by shutdownCtx: if
		// draining outlives the budget, force-stop so shutdown always completes.
		if e.grpcServer != nil {
			stopped := make(chan struct{})
			go func() {
				e.grpcServer.GracefulStop()
				close(stopped)
			}()
			select {
			case <-stopped:
			case <-shutdownCtx.Done():
				e.grpcServer.Stop()
			}
		}

		metricsErr := e.MetricsServer().Shutdown(shutdownCtx)

		// Drain the audit recorder before closing the store so in-flight
		// (detached) writes are not lost. Bounded by shutdownCtx.
		if e.auditRecorder != nil {
			e.auditRecorder.Drain(shutdownCtx)
		}

		var storeErr error
		if e.store != nil {
			storeErr = e.store.Close()
		}

		return multierr.Combine(gatewayErr, apiErr, metricsErr, storeErr)
	}

	health.SetLiveness(true)
	health.SetReadiness(true)

	logging.Info(ctx, "All servers started")
	return nil
}

// Stop gracefully shuts down all servers, background tickers and the repository.
// It is a no-op when Start was never called.
func (e *App) Stop() error {
	if e.stop == nil {
		return nil
	}
	return e.stop()
}
