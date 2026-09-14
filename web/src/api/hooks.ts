import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { components } from "./schema";
import { api, newIdempotencyKey, normalizeApiError, type NormalizedApiError } from "./runtime";

export type Job = components["schemas"]["Job"];
export type Issue = components["schemas"]["Issue"];

export function toApiError(error: unknown): NormalizedApiError {
  if (isNormalizedApiError(error)) {
    return error;
  }
  return normalizeApiError(503, {
    code: "internal.unavailable",
    message: "研究服务暂时不可用，请稍后重试。",
    request_id: "",
    retryable: true,
    issues: [],
  });
}

function isErrorEnvelope(error: unknown): boolean {
  if (typeof error !== "object" || error === null || error instanceof Error) {
    return false;
  }
  const record = error as Record<string, unknown>;
  return typeof record.code === "string" || typeof record.message === "string" || Array.isArray(record.issues);
}

function isNormalizedApiError(error: unknown): error is NormalizedApiError {
  return typeof error === "object" && error !== null && "status" in error && "code" in error && "message" in error;
}

type ApiResult<T> = {
  data?: T;
  error?: unknown;
  response?: Response;
};

export async function unwrap<T>(result: Promise<ApiResult<T>>): Promise<T> {
  const response = await result;
  if (response.error !== undefined) {
    if (isErrorEnvelope(response.error)) {
      throw normalizeApiError(response.response?.status ?? 500, response.error);
    }
    throw toApiError(response.error);
  }
  if (response.data === undefined) {
    throw toApiError(undefined);
  }
  return response.data;
}

function pageQuery(q: string, cursor?: string): { limit: number; sort: "-id"; q?: string; cursor?: string } {
  return {
    limit: 50,
    sort: "-id",
    ...(q ? { q } : {}),
    ...(cursor ? { cursor } : {}),
  };
}

export function useProviders() {
  return useQuery({
    queryKey: ["providers"],
    queryFn: () => unwrap(api.GET("/providers", { params: { query: { limit: 200, sort: "id" } } })),
  });
}

export function useConnections(q: string) {
  return useInfiniteQuery({
    queryKey: ["connections", q],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) => unwrap(api.GET("/connections", { params: { query: pageQuery(q, pageParam) } })),
    getNextPageParam: (page) => page.next_cursor ?? undefined,
  });
}

export function useDatasets(q: string) {
  return useInfiniteQuery({
    queryKey: ["datasets", q],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) => unwrap(api.GET("/datasets", { params: { query: pageQuery(q, pageParam) } })),
    getNextPageParam: (page) => page.next_cursor ?? undefined,
  });
}

export function useBatches() {
  return useInfiniteQuery({
    queryKey: ["batches"],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) => unwrap(api.GET("/batches", { params: { query: pageQuery("", pageParam) } })),
    getNextPageParam: (page) => page.next_cursor ?? undefined,
  });
}

export function useSnapshots() {
  return useInfiniteQuery({
    queryKey: ["snapshots"],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) => unwrap(api.GET("/snapshots", { params: { query: pageQuery("", pageParam) } })),
    getNextPageParam: (page) => page.next_cursor ?? undefined,
  });
}

export function useJobs() {
  return useInfiniteQuery({
    queryKey: ["jobs"],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) => unwrap(api.GET("/jobs", { params: { query: pageQuery("", pageParam) } })),
    getNextPageParam: (page) => page.next_cursor ?? undefined,
    refetchInterval: (query) => {
      const jobs = query.state.data?.pages.flatMap((page) => page.items) ?? [];
      return jobs.some((job) => job.state === "queued" || job.state === "running" || job.state === "cancel_requested")
        ? 2500
        : false;
    },
  });
}

export function useJob(id: string | undefined) {
  return useQuery({
    queryKey: ["job", id],
    enabled: Boolean(id),
    queryFn: () => unwrap(api.GET("/jobs/{id}", { params: { path: { id: id! } } })),
    refetchInterval: (query) => {
      const state = query.state.data?.state;
      return state === "queued" || state === "running" || state === "cancel_requested" ? 2500 : false;
    },
  });
}

export function useCreateConnection() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: components["schemas"]["ConnectionCreate"]) =>
      unwrap(api.POST("/connections", { params: { header: { "Idempotency-Key": newIdempotencyKey() } }, body })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["connections"] }),
  });
}

export function useStartIngestion() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: components["schemas"]["IngestionCreate"]) =>
      unwrap(api.POST("/ingestions", { params: { header: { "Idempotency-Key": newIdempotencyKey() } }, body })),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["jobs"] });
      void queryClient.invalidateQueries({ queryKey: ["batches"] });
    },
  });
}

export function useStartSnapshot() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: components["schemas"]["SnapshotCreate"]) =>
      unwrap(api.POST("/snapshots", { params: { header: { "Idempotency-Key": newIdempotencyKey() } }, body })),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["jobs"] });
      void queryClient.invalidateQueries({ queryKey: ["snapshots"] });
    },
  });
}

export function useCancelJob() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      unwrap(api.POST("/jobs/{id}/cancel", { params: { path: { id }, header: { "Idempotency-Key": newIdempotencyKey() } }, body: {} })),
    onSuccess: (job) => {
      void queryClient.invalidateQueries({ queryKey: ["jobs"] });
      void queryClient.setQueryData(["job", job.id], job);
    },
  });
}

export function useRetryJob() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      unwrap(api.POST("/jobs/{id}/retry", { params: { path: { id }, header: { "Idempotency-Key": newIdempotencyKey() } }, body: {} })),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["jobs"] }),
  });
}

export type Universe = components["schemas"]["Universe"];
export type FactorCatalogEntry = components["schemas"]["Factor"];

export function useUniverses(q: string) {
  return useInfiniteQuery({
    queryKey: ["universes", q],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) => unwrap(api.GET("/universes", { params: { query: pageQuery(q, pageParam) } })),
    getNextPageParam: (page) => page.next_cursor ?? undefined,
  });
}

export function useUniverse(id: string | undefined) {
  return useQuery({
    queryKey: ["universe", id],
    enabled: Boolean(id),
    queryFn: () => unwrap(api.GET("/universes/{id}", { params: { path: { id: id! } } })),
  });
}

// Creation is not idempotent server-side (every save mints a new version);
// the key only makes the HTTP replay of one click safe.
export function useCreateUniverse() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: components["schemas"]["UniverseCreate"]) =>
      unwrap(api.POST("/universes", { params: { header: { "Idempotency-Key": newIdempotencyKey() } }, body })),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["universes"] }),
  });
}

export function useResolveUniverse() {
  return useMutation({
    mutationFn: (input: { id: string; body: components["schemas"]["UniverseResolve"] }) =>
      unwrap(api.POST("/universes/{id}/resolve", { params: { path: { id: input.id } }, body: input.body })),
  });
}

export function useFactors(q: string) {
  return useInfiniteQuery({
    queryKey: ["factors", q],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) => unwrap(api.GET("/factors", { params: { query: pageQuery(q, pageParam) } })),
    getNextPageParam: (page) => page.next_cursor ?? undefined,
  });
}

export function useFactor(ref: components["schemas"]["VersionRef"] | undefined) {
  const id = ref?.id;
  const version = ref?.version;
  return useQuery({
    queryKey: ["factor", id, version],
    enabled: Boolean(id && version),
    queryFn: () => unwrap(api.GET("/factors/{id}", { params: { path: { id: id! }, query: { version: version! } } })),
  });
}

export function usePreflightFactorRun() {
  return useMutation({
    mutationFn: (body: components["schemas"]["FactorRunRequest"]) => unwrap(api.POST("/factor-runs/preflight", { body })),
  });
}

export function useRunFactor() {
  return useMutation({
    mutationFn: (body: components["schemas"]["FactorRunRequest"]) => unwrap(api.POST("/factor-runs", { body })),
  });
}

export function useRunFactorAnalysis() {
  return useMutation({
    mutationFn: (body: components["schemas"]["FactorAnalysisCreate"]) =>
      unwrap(api.POST("/factor-analyses", { params: { header: { "Idempotency-Key": newIdempotencyKey() } }, body })),
  });
}

export type Screener = components["schemas"]["Screener"];
export type ScreenRun = components["schemas"]["ScreenRun"];
export type ScreenRow = components["schemas"]["ScreenRow"];
export type ScreenRowPage = components["schemas"]["ScreenRowPage"];

export function useScreeners(q: string) {
  return useInfiniteQuery({
    queryKey: ["screeners", q],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) => unwrap(api.GET("/screeners", { params: { query: pageQuery(q, pageParam) } })),
    getNextPageParam: (page) => page.next_cursor ?? undefined,
  });
}

/**
 * One immutable screener revision. Both halves of the address are required —
 * an id alone does not name a frozen rule set — so the query stays disabled
 * until the URL carries a version.
 */
export function useScreener(ref: components["schemas"]["VersionRef"] | undefined) {
  const id = ref?.id;
  const version = ref?.version;
  return useQuery({
    queryKey: ["screener", id, version],
    enabled: Boolean(id && version),
    queryFn: () => unwrap(api.GET("/screeners/{id}", { params: { path: { id: id! }, query: { version: version! } } })),
  });
}

// Saving is not idempotent server-side (every save mints a new revision); the
// key only makes the HTTP replay of one click safe.
export function useCreateScreener() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: components["schemas"]["ScreenerCreate"]) =>
      unwrap(api.POST("/screeners", { params: { header: { "Idempotency-Key": newIdempotencyKey() } }, body })),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["screeners"] }),
  });
}

export function usePreflightScreenRun() {
  return useMutation({
    mutationFn: (body: components["schemas"]["ScreenRunCreate"]) => unwrap(api.POST("/screen-runs/preflight", { body })),
  });
}

export function useStartScreenRun() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: components["schemas"]["ScreenRunCreate"]) =>
      unwrap(api.POST("/screen-runs", { params: { header: { "Idempotency-Key": newIdempotencyKey() } }, body })),
    onSuccess: (job) => {
      void queryClient.invalidateQueries({ queryKey: ["jobs"] });
      void queryClient.invalidateQueries({ queryKey: ["screen-runs"] });
      queryClient.setQueryData(["job", job.id], job);
    },
  });
}

export function useScreenRuns(screenerId: string, state: string) {
  return useInfiniteQuery({
    queryKey: ["screen-runs", screenerId, state],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) =>
      unwrap(
        api.GET("/screen-runs", {
          params: {
            query: {
              ...pageQuery("", pageParam),
              ...(screenerId ? { screener_id: screenerId } : {}),
              ...(state ? { state: state as components["schemas"]["Job"]["state"] } : {}),
            },
          },
        }),
      ),
    getNextPageParam: (page) => page.next_cursor ?? undefined,
    // A run's own state is unreadable until it publishes, so the listing polls
    // while the linked jobs are still working.
    refetchInterval: (query) => {
      const runs = query.state.data?.pages.flatMap((page) => page.items) ?? [];
      return runs.some((run) => run.summary === null) ? 2500 : false;
    },
  });
}

export function useScreenRun(id: string | undefined) {
  return useQuery({
    queryKey: ["screen-run", id],
    enabled: Boolean(id),
    queryFn: () => unwrap(api.GET("/screen-runs/{id}", { params: { path: { id: id! } } })),
    // The result is published by a worker; re-read until it lands.
    refetchInterval: (query) => (query.state.data?.summary === null ? 2500 : false),
  });
}

/**
 * Frozen result rows, page by page. The state filter is part of the cache key
 * and of the server-side cursor, so switching it restarts the listing instead
 * of resuming a page that belonged to another filter.
 */
export function useScreenRows(id: string | undefined, state: "" | "selected" | "excluded", enabled: boolean) {
  return useInfiniteQuery({
    queryKey: ["screen-rows", id, state],
    enabled: Boolean(id) && enabled,
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) =>
      unwrap(
        api.GET("/screen-runs/{id}/rows", {
          params: {
            path: { id: id! },
            query: {
              limit: 50,
              ...(state ? { state } : {}),
              ...(pageParam ? { cursor: pageParam } : {}),
            },
          },
        }),
      ),
    getNextPageParam: (page) => page.next_cursor ?? undefined,
  });
}

export function useScreenExplanation(runId: string | undefined, instrumentId: string | undefined) {
  return useQuery({
    queryKey: ["screen-explanation", runId, instrumentId],
    enabled: Boolean(runId && instrumentId),
    queryFn: () =>
      unwrap(
        api.GET("/screen-runs/{id}/explanations/{instrument_id}", {
          params: { path: { id: runId!, instrument_id: instrumentId! } },
        }),
      ),
  });
}

/**
 * Saves the run's complete selection as a new static pool. The request carries
 * only a name: the member list is read server-side from the frozen result, so a
 * page of rows can never become the pool.
 */
export function useSaveScreenUniverse() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: { runId: string; body: components["schemas"]["ScreenUniverseCreate"] }) =>
      unwrap(
        api.POST("/screen-runs/{id}/universe", {
          params: { path: { id: input.runId }, header: { "Idempotency-Key": newIdempotencyKey() } },
          body: input.body,
        }),
      ),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["universes"] }),
  });
}
