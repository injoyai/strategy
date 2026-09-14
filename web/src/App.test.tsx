﻿import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App, { queryClient } from "./App";

/**
 * src/api/runtime.ts builds its singleton client at import time, and
 * openapi-fetch captures globalThis.fetch when the client is created — so a
 * stub installed inside beforeEach or inside a test never takes effect. The
 * module is therefore mocked to build the same client over an injected fetch
 * and an absolute base URL (Node's Request rejects the relative production
 * default).
 */
const { fetchMock, handler } = vi.hoisted(() => ({
  fetchMock: vi.fn(),
  handler: {} as { current: (input: RequestInfo | URL) => Promise<Response> },
}));

vi.mock("./api/runtime", async () => {
  const client = await vi.importActual<typeof import("./api/client")>("./api/client");
  return {
    DEFAULT_API_BASE_URL: client.DEFAULT_API_BASE_URL,
    createApiClient: client.createApiClient,
    newIdempotencyKey: client.newIdempotencyKey,
    normalizeApiError: client.normalizeApiError,
    api: client.createApiClient({ baseUrl: "http://test.local/api/v1", fetch: fetchMock }),
  };
});

const emptyPage = () => Response.json({ items: [], next_cursor: null });

beforeEach(() => {
  window.history.pushState({}, "", "/data");
  // The app query client is module-scoped, so its cache would leak between
  // cases (a later case would read an earlier case's cached page instead of
  // calling the API). Clearing it keeps every case honest.
  queryClient.clear();
  handler.current = async () => emptyPage();
  fetchMock.mockReset();
  fetchMock.mockImplementation((input: RequestInfo | URL) => handler.current(input));
});

afterEach(() => {
  cleanup();
});

describe("App", () => {
  it("renders the data workspace and navigable shell", async () => {
    render(<App />);
    expect(screen.getByRole("heading", { name: "数据中心" })).toBeInTheDocument();
    expect(screen.getByRole("navigation", { name: "工作区" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "数据源" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "发起数据更新" })).toBeInTheDocument();
    // The stub is effective, so the empty catalog is the API's own answer.
    expect(await screen.findByText("还没有数据集")).toBeInTheDocument();
  });

  it("navigates across the M1 routes and updates the document title", async () => {
    render(<App />);

    fireEvent.click(screen.getByRole("link", { name: "数据源" }));
    expect(await screen.findByRole("heading", { name: "数据源" })).toBeInTheDocument();
    expect(document.title).toBe("数据源 — 策略研究工作台");

    fireEvent.click(screen.getByRole("link", { name: "任务中心" }));
    expect(await screen.findByRole("heading", { name: "任务中心" })).toBeInTheDocument();
    expect(document.title).toBe("任务中心 — 策略研究工作台");

    fireEvent.click(screen.getByRole("link", { name: "标的池" }));
    expect(await screen.findByRole("heading", { name: "标的池" })).toBeInTheDocument();
    expect(document.title).toBe("标的池 — 策略研究工作台");

    fireEvent.click(screen.getByRole("link", { name: "因子库" }));
    expect(await screen.findByRole("heading", { name: "因子库" })).toBeInTheDocument();
    expect(document.title).toBe("因子库 — 策略研究工作台");
  });

  it("shows a retryable service error when the API is unavailable", async () => {
    handler.current = async () => {
      throw new TypeError("Failed to fetch");
    };
    render(<App />);

    // The app-level query client retries once with a ~1s backoff, so the
    // error state lands after the default findBy timeout.
    expect(await screen.findAllByText("研究服务暂时不可用，请稍后重试。", {}, { timeout: 5_000 })).toHaveLength(4);
  });
});
