import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ScreenRunPage } from "./ScreenRunPage";

/**
 * The run page must be driven by the server's facts: a result is readable only
 * after the run publishes, and the pool it saves carries the run's complete
 * selection rather than anything the page happens to be showing.
 *
 * src/api/runtime.ts builds its singleton client at import time, so the client
 * is re-created here with an injected fetch and an absolute base URL.
 */
const { fetchMock, calls } = vi.hoisted(() => ({
  fetchMock: vi.fn(),
  calls: [] as { url: string; body: unknown }[],
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

const runId = "srun_1";

function publishedRun() {
  return {
    id: runId,
    job_id: "job_1",
    config: {
      screener_ref: { id: "scr_1", version: "v1" },
      snapshot_id: "snap_1",
      universe_ref: { id: "uni_1", version: "defhash_1" },
      as_of: "2026-01-15T00:00:00Z",
      decision_timezone: "Asia/Shanghai",
      strict_pit: false,
      required_value_policy: "exclude_instrument",
    },
    engine_version: "screening-engine/1",
    scoring_policy_version: "scoring-policy/1",
    config_hash: "confighash",
    snapshot_hash: "manifest_1",
    created_at: "2026-01-15T12:00:00Z",
    summary: {
      population: 2,
      condition_false: 1,
      condition_unknown: 0,
      condition_true: 1,
      rank_insufficient: 0,
      rankable: 1,
      selected: 1,
      not_selected: 0,
      empty_reason: null,
    },
    artifact_ids: ["art_1"],
  };
}

function unpublishedRun() {
  return { ...publishedRun(), summary: null, artifact_ids: [] };
}

function rowsPage() {
  return {
    items: [
      {
        instrument_id: "INST_A",
        symbol: null,
        name: null,
        selected: true,
        rank: 1,
        score: null,
        values: { px: { kind: "decimal", value: "10.40", missing_reason: null } },
        reason: "selected",
        quality_flags: [],
      },
      {
        instrument_id: "INST_B",
        symbol: null,
        name: null,
        selected: false,
        rank: null,
        score: null,
        values: { px: { kind: "decimal", value: null, missing_reason: "missing_value" } },
        reason: "condition_false",
        quality_flags: [],
      },
    ],
    next_cursor: null,
    columns: [{ name: "px", type: "decimal", unit: "cny", nullable: true }],
  };
}

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/screen-runs/${runId}`]}>
        <Routes>
          <Route path="/screen-runs/:id" element={<ScreenRunPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

let runResponse: () => Response;

beforeEach(() => {
  calls.length = 0;
  runResponse = () => Response.json(publishedRun());
  fetchMock.mockReset();
  fetchMock.mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = input instanceof Request ? input.url : String(input);
    // openapi-fetch hands the body either in init or in a constructed Request,
    // so both spellings are read here.
    const rawBody = init?.body
      ? String(init.body)
      : input instanceof Request
        ? await input.clone().text()
        : "";
    const body = rawBody ? (JSON.parse(rawBody) as { name?: string }) : {};
    calls.push({ url, body });
    if (url.includes("/universe")) {
      return Response.json(
        {
          id: "uni_made",
          name: body.name ?? "",
          snapshot_id: "snap_1",
          definition: { kind: "static", members: ["INST_A"] },
          definition_hash: "defhash_made",
          created_at: "2026-01-15T12:00:00Z",
          source: {
            screen_run_id: runId,
            as_of: "2026-01-15T00:00:00Z",
            snapshot_hash: "manifest_1",
            quality_limits: [],
          },
        },
        { status: 201 },
      );
    }
    if (url.includes("/rows")) {
      return Response.json(rowsPage());
    }
    if (/\/universes\/[^?]+/.test(url)) {
      return Response.json({
        id: "uni_made",
        name: "动量池",
        snapshot_id: "snap_1",
        definition: { kind: "static", members: ["INST_A"] },
        definition_hash: "defhash_made",
        created_at: "2026-01-15T12:00:00Z",
        source: null,
      });
    }
    if (url.includes("/screen-runs/")) {
      return runResponse();
    }
    return Response.json({ items: [], next_cursor: null });
  });
});

afterEach(() => {
  cleanup();
});

describe("ScreenRunPage", () => {
  it("shows the frozen configuration, the summary and the published rows", async () => {
    renderPage();
    await screen.findByText(/运行 srun_1/);
    expect(screen.getByText("scr_1@v1")).toBeTruthy();
    expect(screen.getByText("screening-engine/1")).toBeTruthy();
    // Summary counts come from the server; the page never re-derives them.
    expect(screen.getByText("入选").closest(".metric-box")?.textContent).toContain("1");
    const row = await screen.findByText("INST_A");
    expect(row.closest("tr")?.textContent).toContain("入选");
    // A missing display value keeps its reason instead of showing a zero.
    expect(screen.getByText("—（missing_value）")).toBeTruthy();
    // The column header carries the declared unit and its nullability.
    expect(screen.getByText(/cny/)).toBeTruthy();
  });

  it("says the result is not published yet and reads no rows", async () => {
    runResponse = () => Response.json(unpublishedRun());
    renderPage();
    await screen.findByText("结果尚未发布");
    expect(screen.queryByText("INST_A")).toBeNull();
    expect(calls.some((call) => call.url.includes("/rows"))).toBe(false);
  });

  it("saves the pool by name only, never by the rows on screen", async () => {
    renderPage();
    const nameInput = await screen.findByLabelText("标的池名称");
    fireEvent.change(nameInput, { target: { value: "动量池" } });
    fireEvent.click(screen.getByRole("button", { name: /保存为静态标的池/ }));
    await waitFor(() => expect(calls.some((call) => call.url.includes("/universe"))).toBe(true));
    await waitFor(() => expect(screen.getByText(/已保存标的池/)).toBeTruthy());
    const saved = calls.find((call) => call.url.includes("/universe"));
    // The member list is the server's business: the request carries a name and
    // nothing else, so a filtered page can never become the pool.
    expect(saved?.body).toEqual({ name: "动量池" });
  });

  it("refuses to save an empty selection and does not offer the action", async () => {
    runResponse = () =>
      Response.json({ ...publishedRun(), summary: { ...publishedRun().summary, selected: 0, not_selected: 1 } });
    renderPage();
    await screen.findByText("本次运行没有入选标的");
    expect(screen.queryByRole("button", { name: /保存为静态标的池/ })).toBeNull();
  });
});
