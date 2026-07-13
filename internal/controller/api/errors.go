package api

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/logging"
	ruleerrors "github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/errors"
)

// Rule-source labels reported in list/get responses.
const (
	sourceBuiltin = "builtin"
	sourceCustom  = "custom"
)

// toRuleMutationError maps rule use-case sentinels to gRPC status codes:
// ValidationError→InvalidArgument, ErrNotFound→NotFound,
// ErrAlreadyExists→AlreadyExists, ErrBuiltin→FailedPrecondition, and
// ErrTooManyRules→ResourceExhausted (all keep the 4xx client-error class).
// Unknown errors are logged and returned as Internal so store internals never
// leak to callers.
func toRuleMutationError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	var verr *ruleerrors.ValidationError
	switch {
	case errors.As(err, &verr):
		return status.Error(codes.InvalidArgument, verr.Error())
	case errors.Is(err, ruleerrors.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, ruleerrors.ErrAlreadyExists):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, ruleerrors.ErrTooManyRules):
		return status.Error(codes.ResourceExhausted, err.Error())
	case errors.Is(err, ruleerrors.ErrBuiltin):
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		logging.Error(ctx, "rule operation failed", err)
		return status.Error(codes.Internal, "rule operation failed")
	}
}

// internalError logs err server-side and returns a generic Internal status so
// store internals (DSNs, query fragments) never reach the caller.
func internalError(ctx context.Context, msg string, err error) error {
	logging.Error(ctx, msg, err)
	return status.Error(codes.Internal, msg)
}
