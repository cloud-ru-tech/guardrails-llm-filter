import createClient from 'openapi-fetch';

import type { ApiError } from './types';
import type { paths } from './schema';

/**
 * Same-origin client. In production nginx serves the SPA and reverse-proxies
 * `/v1/*` to the target :9080; in dev the Vite proxy does the same. The config
 * API is unauthenticated, so the client calls plain relative `/v1/...` paths
 * with no Authorization header.
 */
export const client = createClient<paths>({ baseUrl: '' });

/**
 * Error carrying the response body and HTTP status. Handles both body shapes:
 * the declared OpenAPI `{ error, details }` and the grpc-gateway
 * `{ code, message, details }` that /v1/* actually emits on RPC errors.
 */
export class ApiRequestError extends Error {
  readonly status: number;
  readonly details?: string;
  /** gRPC status code from the grpc-gateway error body (9 = FailedPrecondition). */
  readonly grpcCode?: number;

  constructor(status: number, body?: ApiError & { message?: string; code?: number }) {
    super(body?.error || body?.message || `Request failed with status ${status}`);
    this.name = 'ApiRequestError';
    this.status = status;
    this.details = body?.details;
    this.grpcCode = body?.code;
  }
}

/**
 * The audit endpoints answer FailedPrecondition → HTTP 400 when
 * GUARDRAILS_AUDIT_ENABLED=false (404 kept for older builds).
 */
export function isAuditDisabledError(err: unknown): boolean {
  return (
    err instanceof ApiRequestError &&
    (err.status === 404 ||
      (err.status === 400 && (err.grpcCode === 9 || /audit trail is disabled/i.test(err.message))))
  );
}

type FetchResult<T> = { data?: T; error?: unknown; response: Response };

/** Unwrap an openapi-fetch result, throwing {@link ApiRequestError} on failure. */
export function unwrap<T>({ data, error, response }: FetchResult<T>): T {
  if (error !== undefined || !response.ok) {
    throw new ApiRequestError(response.status, error as ApiError | undefined);
  }
  return data as T;
}
