package app

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"buf.build/go/protovalidate"
	grpclogging "github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	protovalidate_middleware "github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/protovalidate"
	grpcrecovery "github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	grpcprometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/cloud-ru-tech/guardrails-llm-filter/frontend"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/controller/api"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/logging"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	scanuc "github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/guardrails/scan"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/version"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/tlsutils"

	servicev1 "github.com/cloud-ru-tech/guardrails-llm-filter/pkg/api/proto/cloudru/guardrails/v1/service"
)

// GrpcController builds the gRPC management controller over the use-case layer.
func (e *App) GrpcController(ctx context.Context) *api.Controller {
	if e.grpcController != nil {
		return e.grpcController
	}

	// The interface value must be a nil literal when audit is disabled — a
	// typed-nil *repository.Store would defeat the controller's nil check.
	var auditSvc api.AuditService
	var auditOriginals api.OriginalsDecrypter
	if e.cfg.Audit.Enabled {
		auditSvc = e.Store()
		// Shared codec opens encrypted audit originals on read (pass-through
		// for plaintext / when encryption is disabled).
		auditOriginals = e.storeCodec()
	}

	rulesUC := e.RulesUseCase()

	// Default scan scope: every declared data-type group plus CUSTOM, so a bare
	// scan exercises the whole ruleset.
	scanDataTypes := make([]models.DataType, 0, len(e.DataTypes())+1)
	for _, dt := range e.DataTypes() {
		scanDataTypes = append(scanDataTypes, models.DataType(dt.DataType)) //nolint:gosec // data-type IDs are small
	}
	scanDataTypes = append(scanDataTypes, models.DataTypeCUSTOM)
	scanUC := scanuc.New(scanuc.Deps{
		Production:       e.MaskUseCase(),
		FileRules:        e.loadFileRules(),
		DefaultDataTypes: scanDataTypes,
		KeywordPrefilter: e.cfg.Guardrails.KeywordPrefilterEnabled,
		ParallelMinBytes: e.cfg.Guardrails.MaskParallelMinBytes,
	})

	e.grpcController = api.NewController(api.Deps{
		Create:     rulesUC.Create(),
		Update:     rulesUC.Update(),
		Delete:     rulesUC.Delete(),
		SetEnabled: rulesUC.SetEnabled(),
		Get:        rulesUC.Get(),
		List:       rulesUC.List(),
		Scan:       scanUC,
		Settings:   e.SettingsService(),
		Audit:      auditSvc,
		Originals:  auditOriginals,
		DataTypes:  e.DataTypes(),
		BuildInfo: api.BuildInfo{
			Version:      version.Version,
			Commit:       version.Commit,
			Date:         version.Date,
			Topology:     "standalone",
			StoreBackend: e.cfg.Store.Backend,
		},
	})
	return e.grpcController
}

// GrpcServer builds the management gRPC server with the interceptor chain,
// protovalidate validation and reflection.
func (e *App) GrpcServer(ctx context.Context) *grpc.Server {
	if e.grpcServer != nil {
		return e.grpcServer
	}

	keepAliveParams := keepalive.ServerParameters{
		MaxConnectionIdle:     10 * time.Hour,
		MaxConnectionAge:      24 * time.Hour,
		MaxConnectionAgeGrace: 5 * time.Minute,
		Time:                  60 * time.Second,
		Timeout:               1 * time.Second,
	}

	validator, err := protovalidate.New()
	if err != nil {
		panic(fmt.Errorf("new protovalidate validator: %w", err))
	}

	logger := interceptorLogger()
	logEvents := grpclogging.WithLogOnEvents(grpclogging.FinishCall)

	opts := []grpc.ServerOption{
		grpc.KeepaliveParams(keepAliveParams),
		grpc.ChainUnaryInterceptor(
			grpcrecovery.UnaryServerInterceptor(),
			grpclogging.UnaryServerInterceptor(logger, logEvents),
			grpcprometheus.UnaryServerInterceptor,
			protovalidate_middleware.UnaryServerInterceptor(validator),
		),
		grpc.ChainStreamInterceptor(
			grpcrecovery.StreamServerInterceptor(),
			grpclogging.StreamServerInterceptor(logger, logEvents),
			grpcprometheus.StreamServerInterceptor,
			protovalidate_middleware.StreamServerInterceptor(validator),
		),
	}

	if e.cfg.GrpcSecure {
		logging.Info(ctx, "management gRPC is secure, using self-signed certificate")
		cert, certErr := tlsutils.CreateSelfSignedTLSCertificate()
		if certErr != nil {
			panic(fmt.Errorf("create self-signed certificate: %w", certErr))
		}
		creds := credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
		opts = append(opts, grpc.Creds(creds))
	} else {
		opts = append(opts, grpc.Creds(insecure.NewCredentials()))
	}

	srv := grpc.NewServer(opts...)
	servicev1.RegisterGuardrailsApiServer(srv, e.GrpcController(ctx))
	reflection.Register(srv)

	e.grpcServer = srv
	return e.grpcServer
}

// APIServer returns the management REST server: a grpc-gateway reverse proxy to
// the gRPC listener, plus the REST-only /v1/health and /v1/metrics/summary
// endpoints. Returns nil when the API is disabled (empty API_ADDR).
func (e *App) APIServer(ctx context.Context) *http.Server {
	if e.apiServer != nil {
		return e.apiServer
	}
	if e.cfg.API.Addr == "" {
		return nil
	}
	// The management API is unauthenticated: it exposes mutating endpoints
	// (rules, settings) with no token check, so it must be protected at the
	// network layer (cluster-internal only, never public ingress).
	logging.Warn(ctx, "Management API is unauthenticated; protect it at the network level (no public ingress)")

	mux := runtime.NewServeMux(
		runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONPb{
			// UseProtoNames keeps snake_case field names and UseEnumNumbers keeps
			// data_type as numbers on the wire.
			MarshalOptions:   protojson.MarshalOptions{UseProtoNames: true, UseEnumNumbers: true, EmitUnpopulated: true},
			UnmarshalOptions: protojson.UnmarshalOptions{},
		}),
	)

	dialCreds := grpc.WithTransportCredentials(insecure.NewCredentials())
	if e.cfg.GrpcSecure {
		//nolint:gosec // loopback dial to our own self-signed listener
		dialCreds = grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{InsecureSkipVerify: true}))
	}
	if err := servicev1.RegisterGuardrailsApiHandlerFromEndpoint(
		ctx, mux, dialTarget(e.cfg.GRPCAddr), []grpc.DialOption{dialCreds},
	); err != nil {
		panic(fmt.Errorf("register management gateway: %w", err))
	}

	// REST-only console endpoints not modeled in the proto contract.
	if err := mux.HandlePath("GET", "/v1/health", e.handleHealth); err != nil {
		panic(fmt.Errorf("register /v1/health: %w", err))
	}
	if err := mux.HandlePath("GET", "/v1/metrics/summary", e.handleMetricsSummary); err != nil {
		panic(fmt.Errorf("register /v1/metrics/summary: %w", err))
	}

	e.apiServer = &http.Server{
		Addr:              e.cfg.API.Addr,
		Handler:           e.apiHandler(ctx, mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	return e.apiServer
}

// apiHandler returns the handler for the management API server. When the
// embedded console is enabled and present, it serves the SPA at "/" and routes
// "/v1/*" to the grpc-gateway mux; otherwise it returns the mux unchanged. The
// prefix split is exhaustive: every management route (including the REST-only
// /v1/health and /v1/metrics/summary) lives under /v1/.
func (e *App) apiHandler(ctx context.Context, api http.Handler) http.Handler {
	if !e.cfg.UI.Enabled {
		return api
	}
	if !frontend.Available() {
		// UI requested but the binary was built without a console build (only the
		// dist placeholder is embedded). Serve the API alone.
		logging.Warn(ctx, "Console UI enabled but no build is embedded; serving management API only")
		return api
	}
	logging.Info(ctx, "Serving management console at / on the management API port")
	ui := frontend.Handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			api.ServeHTTP(w, r)
			return
		}
		ui.ServeHTTP(w, r)
	})
}

// handleHealth is a lightweight liveness signal on the management port. It also
// echoes the live mode and store backend so a console can render status.
func (e *App) handleHealth(w http.ResponseWriter, _ *http.Request, _ map[string]string) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":        "ok",
		"mode":          string(e.SettingsService().Global().Mode),
		"store_backend": e.cfg.Store.Backend,
	})
}

// interceptorLogger routes gRPC middleware logs to slog. It logs call metadata
// only (method, code, duration) — never request/response payloads — preserving
// the never-log-secrets invariant.
func interceptorLogger() grpclogging.Logger {
	return grpclogging.LoggerFunc(func(ctx context.Context, lvl grpclogging.Level, msg string, fields ...any) {
		var slvl slog.Level
		switch lvl {
		case grpclogging.LevelDebug:
			slvl = slog.LevelDebug
		case grpclogging.LevelInfo:
			slvl = slog.LevelInfo
		case grpclogging.LevelWarn:
			slvl = slog.LevelWarn
		case grpclogging.LevelError:
			slvl = slog.LevelError
		default:
			slvl = slog.LevelInfo
		}
		slog.Default().Log(ctx, slvl, msg, fields...)
	})
}

// dialTarget makes an in-process dial target from a listen address: a
// wildcard/empty host is rewritten to loopback so the gateway can reach the
// local gRPC listener.
func dialTarget(addr string) string {
	switch {
	case strings.HasPrefix(addr, ":"):
		return "127.0.0.1" + addr
	case strings.HasPrefix(addr, "0.0.0.0:"):
		return "127.0.0.1" + strings.TrimPrefix(addr, "0.0.0.0")
	default:
		return addr
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if payload != nil {
		_ = json.NewEncoder(w).Encode(payload)
	}
}
