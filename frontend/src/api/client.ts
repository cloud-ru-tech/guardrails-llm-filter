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

/** Error carrying the API's `{ error, details }` body and HTTP status. */
export class ApiRequestError extends Error {
  readonly status: number;
  readonly details?: string;

  constructor(status: number, body?: ApiError) {
    super(body?.error || `Request failed with status ${status}`);
    this.name = 'ApiRequestError';
    this.status = status;
    this.details = body?.details;
  }
}

type FetchResult<T> = { data?: T; error?: unknown; response: Response };

/** Unwrap an openapi-fetch result, throwing {@link ApiRequestError} on failure. */
export function unwrap<T>({ data, error, response }: FetchResult<T>): T {
  if (error !== undefined || !response.ok) {
    throw new ApiRequestError(response.status, error as ApiError | undefined);
  }
  return data as T;
}
