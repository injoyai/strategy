import createClient, { type Middleware } from "openapi-fetch";
import type { components, paths } from "./schema";

/**
 * Typed HTTP client for the researchd contract.
 *
 * The shape of every path and payload comes from src/api/schema.d.ts, which
 * is generated from docs/api/openapi.json (`npm run gen:api`) - never edit it
 * by hand. Every request carries an X-Request-ID so a server-side failure can
 * be correlated with the request_id field of the error envelope.
 */

export type ApiError = components["schemas"]["Error"];

export interface CreateApiClientOptions {
  /** API base URL; defaults to the same-origin contract mount point. */
  baseUrl?: string;
  /** Bearer token for auth.mode=bearer deployments; omit in local mode. */
  bearerToken?: string;
}

export type ApiClient = ReturnType<typeof createApiClient>;

export const DEFAULT_API_BASE_URL = "/api/v1";

/** Generate a new key for one user-visible write operation. */
export function newIdempotencyKey(): string {
  return `web_${crypto.randomUUID()}`;
}

export function createApiClient(options: CreateApiClientOptions = {}) {
  const { baseUrl = DEFAULT_API_BASE_URL, bearerToken } = options;
  const client = createClient<paths>({ baseUrl });

  const headers: Middleware = {
    onRequest({ request }) {
      request.headers.set("X-Request-ID", crypto.randomUUID());
      if (bearerToken) {
        request.headers.set("Authorization", `Bearer ${bearerToken}`);
      }
      if (["POST", "PUT", "PATCH", "DELETE"].includes(request.method) && !request.headers.has("Idempotency-Key")) {
        request.headers.set("Idempotency-Key", newIdempotencyKey());
      }
      return request;
    },
  };
  client.use(headers);
  return client;
}

/** A contract error envelope plus the HTTP status it arrived with. */
export type NormalizedApiError = ApiError & { status: number };

const fallbackError: ApiError = {
  code: "internal.error",
  message: "unexpected response shape",
  request_id: "",
  retryable: false,
  issues: [],
};

/**
 * Normalize any non-2xx response body into the contract Error shape. Defends
 * against proxies or stale servers replying with HTML or legacy payloads so
 * UI code can always read code/message/request_id.
 */
export function normalizeApiError(status: number, body: unknown): NormalizedApiError {
  const base: NormalizedApiError = { ...fallbackError, issues: [], status };
  if (typeof body !== "object" || body === null) {
    return base;
  }
  const record = body as Record<string, unknown>;
  if (typeof record.code === "string") {
    base.code = record.code;
  }
  if (typeof record.message === "string") {
    base.message = record.message;
  }
  if (typeof record.request_id === "string") {
    base.request_id = record.request_id;
  }
  if (typeof record.retryable === "boolean") {
    base.retryable = record.retryable;
  }
  if (Array.isArray(record.issues)) {
    base.issues = record.issues as ApiError["issues"];
  }
  return base;
}
