// Package api is the gRPC implementation of the management API
// (GuardrailsApi): custom rule CRUD, global settings, data-type discovery and
// dry-run masking checks. REST is served from the same contract via
// grpc-gateway (wired in internal/app). It is separate from the data-plane
// proxy in internal/controller/gateway, which stays plain HTTP.
package api

import (
	"context"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/guardrails/scan"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/create"
	deleterule "github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/delete"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/get"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/list"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/setenabled"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/update"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"

	servicev1 "github.com/cloud-ru-tech/guardrails-llm-filter/pkg/api/proto/cloudru/guardrails/v1/service"
)

// SettingsService is the global-settings dependency of the API.
type SettingsService interface {
	Global() models.GuardrailsSettings
	Update(ctx context.Context, gs models.GuardrailsSettings) error
}

// ScanHandler runs a dry-run masking scan.
type ScanHandler interface {
	Handle(ctx context.Context, cmd scan.Command) (scan.CommandResponse, error)
}

// AuditService is the masking-audit dependency of the API. The method set
// matches repository.AuditStore, so the store satisfies it directly. A nil
// service means the audit trail is disabled and the audit RPCs report
// FailedPrecondition.
type AuditService interface {
	GetAuditRecord(ctx context.Context, requestID string) (models.AuditRecord, error)
	ListAuditRecords(ctx context.Context, q repository.AuditQuery) (repository.AuditPage, error)
}

// OriginalsDecrypter opens encrypted audit-replacement originals on the read
// path (statecodec.Codec satisfies it). A plaintext value passes through; an
// undecryptable value is dropped by the caller. nil disables decryption.
type OriginalsDecrypter interface {
	DecryptString(value string) (string, error)
}

// BuildInfo is the static service identity reported by GetVersion. Topology is
// "standalone"; StoreBackend is the configured persistence backend name.
type BuildInfo struct {
	Version      string
	Commit       string
	Date         string
	Topology     string
	StoreBackend string
}

// Deps are the dependencies of the gRPC controller. The rule handlers reuse the
// existing per-scenario use-case CommandHandler interfaces.
type Deps struct {
	Create     create.CommandHandler
	Update     update.CommandHandler
	Delete     deleterule.CommandHandler
	SetEnabled setenabled.CommandHandler
	Get        get.CommandHandler
	List       list.CommandHandler
	Scan       ScanHandler
	Settings   SettingsService
	Audit      AuditService       // nil = audit disabled
	Originals  OriginalsDecrypter // nil = no decryption of audit originals
	DataTypes  []rule.DataType
	BuildInfo  BuildInfo
}

// Controller implements servicev1.GuardrailsApiServer over the use-case layer.
type Controller struct {
	servicev1.UnimplementedGuardrailsApiServer

	create     create.CommandHandler
	update     update.CommandHandler
	delete     deleterule.CommandHandler
	setEnabled setenabled.CommandHandler
	get        get.CommandHandler
	list       list.CommandHandler
	scan       ScanHandler
	settings   SettingsService
	audit      AuditService
	originals  OriginalsDecrypter
	dataTypes  []rule.DataType
	buildInfo  BuildInfo
}

// NewController wires the gRPC controller from its dependencies.
func NewController(d Deps) *Controller {
	return &Controller{
		create:     d.Create,
		update:     d.Update,
		delete:     d.Delete,
		setEnabled: d.SetEnabled,
		get:        d.Get,
		list:       d.List,
		scan:       d.Scan,
		settings:   d.Settings,
		audit:      d.Audit,
		originals:  d.Originals,
		dataTypes:  d.DataTypes,
		buildInfo:  d.BuildInfo,
	}
}
