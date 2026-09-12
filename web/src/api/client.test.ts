import { afterEach, describe, expect, it, vi } from "vitest";
import { createApiClient, newIdempotencyKey, normalizeApiError } from "./client";

const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

interface CapturedRequest {
  url: string;
  method: string;
  headers: Headers;
}

// Node's Request rejects relative URLs, so tests use an absolute base URL.
// The production default ("/api/v1") is resolved by the browser at runtime.
function stubFetch(status: number, body: unknown): CapturedRequest[] {
  const requests: CapturedRequest[] = [];
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const request = input instanceof Request ? input : new Request(input, init);
    requests.push({ url: request.url, method: request.method, headers: request.headers });
    return new Response(JSON.stringify(body), {
      status,
      headers: { "Content-Type": "application/json" },
    });
  });
  vi.stubGlobal("fetch", fetchMock);
  return requests;
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("createApiClient", () => {
  it("prefixes the base URL and injects a request id on every request", async () => {
    const requests = stubFetch(200, { items: [] });
    const client = createApiClient({ baseUrl: "http://test.local/api/v1" });

    const { data, error } = await client.GET("/providers");
    expect(error).toBeUndefined();
    expect(data).toEqual({ items: [] });

    expect(requests).toHaveLength(1);
    expect(requests[0]!.url).toBe("http://test.local/api/v1/providers");
    expect(requests[0]!.method).toBe("GET");
    expect(requests[0]!.headers.get("x-request-id")).toMatch(UUID_PATTERN);
    expect(requests[0]!.headers.get("authorization")).toBeNull();
  });

  it("injects the bearer token and a distinct request id per call", async () => {
    const requests = stubFetch(200, {});
    const client = createApiClient({ baseUrl: "http://test.local/api/v1", bearerToken: "secret-token" });

    await client.GET("/providers");
    await client.GET("/providers");

    expect(requests).toHaveLength(2);
    const ids = requests.map((request) => request.headers.get("x-request-id"));
    for (const id of ids) {
      expect(id).toMatch(UUID_PATTERN);
    }
    expect(new Set(ids).size).toBe(2);
    for (const request of requests) {
      expect(request.headers.get("authorization")).toBe("Bearer secret-token");
    }
  });

  it("preserves the idempotency key supplied for a write operation", async () => {
    const requests = stubFetch(201, {});
    const client = createApiClient({ baseUrl: "http://test.local/api/v1" });

    await client.POST("/connections", {
      params: { header: { "Idempotency-Key": "connection-create-1" } },
      body: {
        name: "synthetic",
        provider_ref: { id: "synthetic", version: "v1" },
        settings: {},
      },
    });

    expect(requests[0]!.method).toBe("POST");
    expect(requests[0]!.headers.get("idempotency-key")).toBe("connection-create-1");
    expect(requests[0]!.headers.get("x-request-id")).toMatch(UUID_PATTERN);
  });

  it("creates browser-safe idempotency keys", () => {
    expect(newIdempotencyKey()).toMatch(/^web_[0-9a-f-]{36}$/i);
  });
});

describe("normalizeApiError", () => {
  it("keeps the contract fields of a valid envelope", () => {
    const normalized = normalizeApiError(404, {
      code: "resource.not_found",
      message: "no such resource",
      request_id: "req_1",
      retryable: false,
      issues: [],
    });
    expect(normalized).toEqual({
      code: "resource.not_found",
      message: "no such resource",
      request_id: "req_1",
      retryable: false,
      issues: [],
      status: 404,
    });
  });

  it("keeps retryable and issue details when present", () => {
    const normalized = normalizeApiError(429, {
      code: "rate.limited",
      message: "slow down",
      request_id: "req_2",
      retryable: true,
      issues: [
        {
          code: "rate.limited",
          path: "",
          message: "slow down",
          severity: "error",
          details: null,
        },
      ],
    });
    expect(normalized.status).toBe(429);
    expect(normalized.retryable).toBe(true);
    expect(normalized.issues).toHaveLength(1);
  });

  it("falls back to a generic error for non-envelope bodies", () => {
    for (const body of ["oops", null, 42, undefined]) {
      const normalized = normalizeApiError(502, body);
      expect(normalized).toEqual({
        code: "internal.error",
        message: "unexpected response shape",
        request_id: "",
        retryable: false,
        issues: [],
        status: 502,
      });
    }
  });

  it("fills only the missing fields of a partial envelope", () => {
    const normalized = normalizeApiError(500, { code: "internal.error" });
    expect(normalized.code).toBe("internal.error");
    expect(normalized.message).toBe("unexpected response shape");
    expect(normalized.request_id).toBe("");
    expect(normalized.retryable).toBe(false);
    expect(normalized.issues).toEqual([]);
    expect(normalized.status).toBe(500);
  });
});
