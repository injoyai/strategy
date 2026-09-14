import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { FactorsPage } from "./FactorsPage";

/**
 * M1-09 frontend closure: the factor page must drive a synchronous run end to
 * end — success, failure and retry — against the real client and contract
 * error shapes.
 *
 * src/api/runtime.ts builds its singleton client at import time, so the test
 * cannot patch a global after the fact. Instead the module is mocked to build
 * the same client with an injected fetch and an absolute base URL (Node's
 * Request rejects the relative production default).
 */
const { fetchMock, responses } = vi.hoisted(() => ({
  fetchMock: vi.fn(),
  responses: {
    preflight: () => Response.json({}),
    run: () => Response.json({}),
    factorDetail: (_id: string) => Response.json({}),
  },
}));

vi.mock("../../api/runtime", async () => {
  const client = await vi.importActual<typeof import("../../api/client")>("../../api/client");
  return {
    DEFAULT_API_BASE_URL: client.DEFAULT_API_BASE_URL,
    createApiClient: client.createApiClient,
    newIdempotencyKey: client.newIdempotencyKey,
    normalizeApiError: client.normalizeApiError,
    api: client.createApiClient({ baseUrl: "http://test.local/api/v1", fetch: fetchMock }),
  };
});

const snapshot = {
  id: "snap_1",
  name: "快照一",
  batch_ids: ["batch_1"],
  strict_pit: true,
  manifest_hash: "manifest_1",
  created_at: "2026-01-15T12:00:00Z",
  quality_issues: [],
};

const universe = {
  id: "uni_1",
  name: "测试池",
  snapshot_id: "snap_1",
  definition: { kind: "static" as const, members: ["INST_A", "INST_B"] },
  definition_hash: "defhash_1",
  created_at: "2026-01-15T12:00:00Z",
};

const factor = {
  id: "momentum",
  version: "1",
  title: "动量",
  kind: "builtin" as const,
  params: [{ name: "n", type: "integer" as const, required: true }],
  inputs: [{ name: "close", dataset: "bar", field: "close", frequency: "1d", lookback: 1, unit: "price", pit: true }],
  dependencies: [],
  output_unit: "ratio",
  asset_classes: ["equity"],
};

function frame(covered: number, total: number) {
  return {
    factor_ref: { id: "momentum", version: "1" },
    snapshot_id: "snap_1",
    universe_id: "uni_1",
    as_of: "2026-01-15T00:00:00Z",
    covered,
    total,
    members: [
      { instrument_id: "INST_A", value: "0.25", missing_reason: null },
      { instrument_id: "INST_B", value: null, missing_reason: "insufficient_history" },
    ],
  };
}

let preflightCalls = 0;
let runCalls = 0;

function jsonError(status: number, code: string, message: string, retryable = false): Response {
  return Response.json({ code, message, request_id: "req_test", retryable, issues: [] }, { status });
}

function renderPage(entry = "/factors") {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[entry]}>
        <Routes>
          <Route path="/factors" element={<FactorsPage />} />
          <Route path="/factors/:id" element={<FactorsPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

function chooseOption(label: string, optionText: string) {
  const control = screen.getByLabelText(label);
  fireEvent.mouseDown(control.closest(".ant-select-selector") ?? control);
  fireEvent.click(screen.getByTitle(optionText));
}

async function fillRunForm() {
  await screen.findByText("动量");
  chooseOption("快照", "快照一 · snap_1");
  chooseOption("标的池版本", "测试池 · uni_1");
  chooseOption("因子版本", "动量 · momentum@1");
  fireEvent.change(screen.getByLabelText("决策时点 as_of"), { target: { value: "2026-01-15T00:00:00Z" } });
  fireEvent.change(screen.getByLabelText("窗口起点 window_from"), { target: { value: "2025-11-01T00:00:00Z" } });
}

beforeEach(() => {
  preflightCalls = 0;
  runCalls = 0;
  responses.preflight = () => Response.json({ valid: true, issues: [], estimated_rows: null });
  responses.run = () => Response.json(frame(1, 2));
  responses.factorDetail = () => Response.json(factor);
  fetchMock.mockReset();
  fetchMock.mockImplementation(async (input: RequestInfo | URL) => {
    const url = input instanceof Request ? input.url : String(input);
    if (url.includes("/factor-runs/preflight")) {
      preflightCalls += 1;
      return responses.preflight();
    }
    if (url.includes("/factor-runs")) {
      runCalls += 1;
      return responses.run();
    }
    const detail = /\/factors\/([^?]+)/.exec(url);
    if (detail) return responses.factorDetail(decodeURIComponent(detail[1]!));
    if (url.includes("/factors")) return Response.json({ items: [factor], next_cursor: null });
    if (url.includes("/snapshots")) return Response.json({ items: [snapshot], next_cursor: null });
    if (url.includes("/universes")) return Response.json({ items: [universe], next_cursor: null });
    return Response.json({ items: [], next_cursor: null });
  });
});

afterEach(() => {
  cleanup();
});

describe("FactorsPage", () => {
  it("runs a factor and renders the cross-section on success", async () => {
    renderPage();
    await fillRunForm();

    fireEvent.click(screen.getByRole("button", { name: /预\s*检/ }));
    expect(await screen.findByText("预检通过：可以运行该因子。")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "运行因子" }));
    expect(await screen.findByText(/横截面已计算：覆盖 1 \/ 2/)).toBeInTheDocument();
    expect(screen.getByText("0.25")).toBeInTheDocument();
    expect(screen.getByText("insufficient_history")).toBeInTheDocument();
    expect(runCalls).toBe(1);
  });

  it("surfaces preflight findings as a failure the caller can fix in one round", async () => {
    responses.preflight = () =>
      Response.json({
        valid: false,
        issues: [{ code: "factor.insufficient_history", path: "bar:close", message: "成员缺少预热历史", severity: "error", details: {} }],
        estimated_rows: null,
      });
    renderPage();
    await fillRunForm();

    fireEvent.click(screen.getByRole("button", { name: /预\s*检/ }));
    expect(await screen.findByText(/预检发现 1 个问题/)).toBeInTheDocument();
    expect(screen.getByText("factor.insufficient_history")).toBeInTheDocument();
    expect(screen.getByText("成员缺少预热历史")).toBeInTheDocument();
  });

  it("shows the contract error and retries the exact same request", async () => {
    responses.run = () => jsonError(422, "factor.preflight_failed", "快照与标的池版本的绑定不一致。");
    renderPage();
    await fillRunForm();

    fireEvent.click(screen.getByRole("button", { name: "运行因子" }));
    expect(await screen.findByText("快照与标的池版本的绑定不一致。")).toBeInTheDocument();
    expect(screen.getByText("错误码：factor.preflight_failed")).toBeInTheDocument();

    // Retry must replay the request that failed, not re-read the form.
    responses.run = () => Response.json(frame(2, 2));
    fireEvent.click(screen.getByRole("button", { name: "重新读取" }));

    expect(await screen.findByText(/横截面已计算：覆盖 2 \/ 2/)).toBeInTheDocument();
    await waitFor(() => expect(runCalls).toBe(2));
  });

  it("reports a retryable transport failure during preflight", async () => {
    responses.preflight = () => jsonError(503, "internal.unavailable", "研究服务暂时不可用，请稍后重试。", true);
    renderPage();
    await fillRunForm();

    fireEvent.click(screen.getByRole("button", { name: /预\s*检/ }));
    expect(await screen.findByText("研究服务暂时不可用，请稍后重试。")).toBeInTheDocument();
    expect(screen.getByText("错误码：internal.unavailable")).toBeInTheDocument();
  });

  it("opens the factor addressed by the URL and back-links its dependencies", async () => {
    const composite = { ...factor, title: "复合动量", dependencies: [{ id: "close_price", version: "1" }] };
    const dependency = { ...factor, id: "close_price", title: "收盘价", params: [], dependencies: [] };
    responses.factorDetail = (id) => Response.json(id === "close_price" ? dependency : composite);
    renderPage("/factors/momentum?version=1");

    expect(await screen.findByText("复合动量")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("link", { name: "close_price@1" }));
    expect(await screen.findByText("收盘价")).toBeInTheDocument();
  });
});
