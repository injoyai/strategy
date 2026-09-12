import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Job } from "./hooks";
import { useJobEventStream } from "./sse";

const job: Job = {
  id: "job_1",
  run_id: "run_1",
  parent_job_id: null,
  kind: "ingestion.run",
  state: "running",
  phase: "fetching",
  completed: 1,
  total: 2,
  created_at: "2026-09-12T00:00:00Z",
  updated_at: "2026-09-12T00:00:01Z",
  error: null,
  result_refs: [],
};

function eventStream(event: string): ReadableStream<Uint8Array> {
  return new ReadableStream({
    start(controller) {
      controller.enqueue(new TextEncoder().encode(event));
      controller.close();
    },
  });
}

function jobEvent(sequence: string): string {
  return [
    `id: ${sequence}`,
    `data: ${JSON.stringify({ job_id: job.id, sequence, at: job.updated_at, job })}`,
    "",
    "",
  ].join("\n");
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("useJobEventStream", () => {
  it("parses a job event and sends the event-stream request headers", async () => {
    const requests: { url: string; headers: Headers }[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      requests.push({ url: String(input), headers: new Headers(init?.headers) });
      return new Response(eventStream(jobEvent("7")), {
        status: 200,
        headers: { "Content-Type": "text/event-stream" },
      });
    }));
    const onJob = vi.fn();
    const onStatus = vi.fn();
    const result = renderHook(() => useJobEventStream(job.id, onJob, onStatus));

    await waitFor(() => expect(onJob).toHaveBeenCalledWith(job));
    expect(requests[0]!.url).toBe("/api/v1/jobs/job_1/events");
    expect(requests[0]!.headers.get("accept")).toBe("text/event-stream");
    expect(requests[0]!.headers.get("x-request-id")).toMatch(/^[0-9a-f-]{36}$/i);
    expect(requests[0]!.headers.get("last-event-id")).toBeNull();
    expect(onStatus).toHaveBeenCalledWith("connected");
    result.unmount();
  });

  it("clears an expired replay cursor before reconnecting after 410", async () => {
    const requests: { headers: Headers }[] = [];
    let attempt = 0;
    vi.stubGlobal("fetch", vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      requests.push({ headers: new Headers(init?.headers) });
      attempt += 1;
      if (attempt === 1) return new Response(null, { status: 410 });
      return new Response(eventStream(jobEvent("1")), {
        status: 200,
        headers: { "Content-Type": "text/event-stream" },
      });
    }));
    const onJob = vi.fn();
    const onStatus = vi.fn();
    const result = renderHook(() => useJobEventStream(job.id, onJob, onStatus));

    await waitFor(() => expect(onJob).toHaveBeenCalledWith(job), { timeout: 1000 });
    expect(requests).toHaveLength(2);
    expect(requests[0]!.headers.get("last-event-id")).toBeNull();
    expect(requests[1]!.headers.get("last-event-id")).toBeNull();
    expect(onStatus).toHaveBeenCalledWith("expired");
    result.unmount();
  });
});
