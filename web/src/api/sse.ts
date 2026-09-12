import { useEffect, useRef } from "react";
import type { Job } from "./hooks";
import { DEFAULT_API_BASE_URL } from "./runtime";

type StreamStatus = "connecting" | "connected" | "reconnecting" | "expired" | "unavailable";

type JobEventPayload = {
  job_id: string;
  sequence: string;
  at: string;
  job: Job;
};

export function useJobEventStream(
  jobId: string | undefined,
  onJob: (job: Job) => void,
  onStatus: (status: StreamStatus) => void,
) {
  const onJobRef = useRef(onJob);
  const onStatusRef = useRef(onStatus);
  onJobRef.current = onJob;
  onStatusRef.current = onStatus;

  useEffect(() => {
    if (!jobId) return;
    const selectedJobId = jobId;
    const controller = new AbortController();
    let lastEventId: string | undefined;
    let attempt = 0;

    async function connect() {
      while (!controller.signal.aborted) {
        try {
          onStatusRef.current("connecting");
          const headers: Record<string, string> = {
            Accept: "text/event-stream",
            "X-Request-ID": crypto.randomUUID(),
          };
          if (lastEventId) headers["Last-Event-ID"] = lastEventId;
          const response = await fetch(`${DEFAULT_API_BASE_URL}/jobs/${encodeURIComponent(selectedJobId)}/events`, {
            headers,
            signal: controller.signal,
          });
          if (response.status === 410) {
            // The current Job query remains the source of truth; clear the replay cursor and reconnect.
            lastEventId = undefined;
            attempt = 0;
            onStatusRef.current("expired");
            await wait(250);
            continue;
          }
          if (!response.ok || !response.body) {
            throw new Error(`event stream unavailable: ${response.status}`);
          }
          attempt = 0;
          onStatusRef.current("connected");
          await readStream(response.body, (event) => {
            if (event.id) lastEventId = event.id;
            if (!event.data) return;
            try {
              const payload: unknown = JSON.parse(event.data);
              if (isJobEvent(payload) && payload.job_id === selectedJobId) onJobRef.current(payload.job);
            } catch {
              // Ignore malformed event frames; the current Job read will still recover state.
            }
          }, controller.signal);
          if (!controller.signal.aborted) onStatusRef.current("reconnecting");
        } catch (error) {
          if (controller.signal.aborted || (error instanceof DOMException && error.name === "AbortError")) break;
          onStatusRef.current("unavailable");
        }
        attempt += 1;
        await wait(Math.min(8000, 500 * 2 ** Math.min(attempt, 4)));
      }
    }

    void connect();
    return () => controller.abort();
  }, [jobId]);
}

async function readStream(body: ReadableStream<Uint8Array>, onEvent: (event: ParsedEvent) => void, signal: AbortSignal) {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let event: ParsedEvent = { data: "" };
  try {
    while (!signal.aborted) {
      const chunk = await reader.read();
      if (chunk.done) break;
      buffer += decoder.decode(chunk.value, { stream: true });
      const lines = buffer.split(/\r?\n/);
      buffer = lines.pop() ?? "";
      for (const line of lines) {
        if (line === "") {
          if (event.data) onEvent({ ...event, data: event.data.endsWith("\n") ? event.data.slice(0, -1) : event.data });
          event = { data: "" };
        } else if (line.startsWith("id:")) {
          event.id = line.slice(3).trim();
        } else if (line.startsWith("data:")) {
          event.data += `${line.slice(5).trimStart()}\n`;
        }
      }
    }
  } finally {
    reader.releaseLock();
  }
}

type ParsedEvent = { id?: string; data: string };

function isJobEvent(value: unknown): value is JobEventPayload {
  if (typeof value !== "object" || value === null) return false;
  const record = value as Record<string, unknown>;
  return typeof record.job_id === "string" && typeof record.sequence === "string" && typeof record.at === "string" && typeof record.job === "object" && record.job !== null;
}

function wait(milliseconds: number) {
  return new Promise<void>((resolve) => window.setTimeout(resolve, milliseconds));
}
