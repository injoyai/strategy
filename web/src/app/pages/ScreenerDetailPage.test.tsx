import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ScreenerDetailPage } from "./ScreenerDetailPage";

/**
 * The screener detail page both reads the frozen revision and launches runs, so
 * two things have to hold: the frozen rules are shown as saved, and a preflight
 * that passes with findings shows those findings instead of silently saying
 * "ok" — a member the factor cannot compute is exactly the caveat an operator
 * has to see before submitting.
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

const screener = {
  id: "scr_1",
  version: "v1",
  name: "便宜动量池",
  description: "示例方案",
  rule_schema_version: "screener-rule/1",
  created_at: "2026-01-15T12:00:00Z",
  parent_id: null,
  input_bindings: [
    { binding_id: "px", kind: "field" as const, dataset: "bar", field: "close" },
    { binding_id: "mom", kind: "factor" as const, factor_ref: { id: "momentum", version: "1.0.0" }, params: { n: 1 } },
  ],
  condition_tree: {
    node_id: "cheap",
    kind: "compare" as const,
    input: { binding_id: "px" },
    operator: "lt" as const,
    value: { kind: "decimal" as const, value: "15", missing_reason: null },
  },
  ranking: {
    mode: "score" as const,
    components: [{ input: { binding_id: "mom" }, weight: "1", direction: "larger_is_better" as const }],
  },
  selection: { mode: "all" as const },
  display_columns: ["px", "mom"],
};

const snapshot = {
  id: "snap_1",
  name: "快照一",
  batch_ids: ["batch_1"],
  strict_pit: false,
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

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={["/screeners/scr_1?version=v1"]}>
        <Routes>
          <Route path="/screeners/:id" element={<ScreenerDetailPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

let preflightResponse: () => Response;

// The select lists come from their own queries and Ant renders options lazily on
// open, so the helper opens the dropdown first and then waits for the option's
// label to appear.
async function chooseOption(label: string, optionText: string) {
  const control = screen.getByLabelText(label);
  fireEvent.mouseDown(control.closest(".ant-select-selector") ?? control);
  const option = await waitFor(() => {
    const found = screen.getAllByText(optionText).find((node) => node.classList.contains("ant-select-item-option-content"));
    expect(found).toBeTruthy();
    return found!;
  });
  fireEvent.click(option);
}

async function fillRunForm() {
  await screen.findByText("便宜动量池");
  await chooseOption("快照", "快照一 · snap_1");
  await chooseOption("标的池版本", "测试池 · uni_1 · 静态");
  fireEvent.change(screen.getByLabelText("决策时点 as_of"), { target: { value: "2026-01-15T00:00:00Z" } });
}

beforeEach(() => {
  calls.length = 0;
  preflightResponse = () => Response.json({ valid: true, issues: [], coverage: [], estimated_scan_rows: null, estimated_rows: 2 });
  fetchMock.mockReset();
  fetchMock.mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = input instanceof Request ? input.url : String(input);
    const rawBody = init?.body ? String(init.body) : input instanceof Request ? await input.clone().text() : "";
    calls.push({ url, body: rawBody ? JSON.parse(rawBody) : undefined });
    if (url.includes("/screen-runs/preflight")) return preflightResponse();
    if (url.includes("/screeners/")) return Response.json(screener);
    if (url.includes("/snapshots")) return Response.json({ items: [snapshot], next_cursor: null });
    if (url.includes("/universes")) return Response.json({ items: [universe], next_cursor: null });
    return Response.json({ items: [], next_cursor: null });
  });
});

afterEach(() => {
  cleanup();
});

describe("ScreenerDetailPage", () => {
  it("shows the frozen rule set as saved", async () => {
    renderPage();
    await screen.findByText("便宜动量池");
    expect(screen.getByText("scr_1@v1")).toBeTruthy();
    // the condition, the ranking and the bindings are all rendered read-only
    expect(screen.getByText(/px < 15/)).toBeTruthy();
    expect(screen.getByText(/momentum@1.0.0/)).toBeTruthy();
    expect(screen.getByText(/越大越好/)).toBeTruthy();
  });

  it("shows the findings of a passing preflight instead of only saying ok", async () => {
    preflightResponse = () =>
      Response.json({
        valid: true,
        issues: [
          {
            code: "insufficient_history",
            path: "input_bindings[mom]",
            message: 'input "close": 1 member(s) have fewer than 2 usable points (INST_B)',
            severity: "warning",
          },
        ],
        coverage: [
          { binding_id: "px", available: true, reason: null },
          { binding_id: "mom", available: true, reason: null },
        ],
        estimated_scan_rows: null,
        estimated_rows: 2,
      });
    renderPage();
    await fillRunForm();
    fireEvent.click(screen.getByRole("button", { name: /预\s*检/ }));
    await waitFor(() => expect(screen.getByText(/预检通过，但有 1 条提示/)).toBeTruthy());
    expect(screen.getByText(/INST_B/)).toBeTruthy();
  });

  it("reports a preflight that does not pass as a refusal", async () => {
    preflightResponse = () =>
      Response.json({
        valid: false,
        issues: [
          { code: "screening.literal_kind_mismatch", path: "condition_tree.literal", message: "literal kind mismatch", severity: "error" },
        ],
        coverage: [],
        estimated_scan_rows: null,
        estimated_rows: null,
      });
    renderPage();
    await fillRunForm();
    fireEvent.click(screen.getByRole("button", { name: /预\s*检/ }));
    await waitFor(() => expect(screen.getByText(/预检未通过/)).toBeTruthy());
    expect(screen.getByText(/screening.literal_kind_mismatch/)).toBeTruthy();
  });

  it("submits the frozen reference, not the editable draft", async () => {
    renderPage();
    await fillRunForm();
    fireEvent.click(screen.getByRole("button", { name: /提交运行/ }));
    await waitFor(() => expect(calls.some((call) => call.url.endsWith("/screen-runs"))).toBe(true));
    const submitted = calls.find((call) => call.url.endsWith("/screen-runs"))?.body as Record<string, unknown>;
    expect(submitted.screener_ref).toEqual({ id: "scr_1", version: "v1" });
    // The pool's version pin is its definition hash, so the run cannot drift from
    // the membership it selected.
    expect(submitted.universe_ref).toEqual({ id: "uni_1", version: "defhash_1" });
  });

  it("keeps every input when a preflight is refused", async () => {
    preflightResponse = () =>
      Response.json({
        valid: false,
        issues: [
          { code: "screenrun.unit_mismatch", path: "input_bindings[mom]", message: "unit mismatch", severity: "error" },
        ],
        coverage: [],
        estimated_scan_rows: null,
        estimated_rows: null,
      });
    renderPage();
    await fillRunForm();
    fireEvent.change(screen.getByLabelText("决策时区"), { target: { value: "Asia/Tokyo" } });
    fireEvent.click(screen.getByRole("button", { name: /预\s*检/ }));
    await waitFor(() => expect(screen.getByText(/预检未通过/)).toBeTruthy());
    // A refusal is exactly when the operator has to fix one finding and retry, so
    // the draft has to survive it: losing the snapshot, pool, time and zone would
    // force re-entering the whole request.
    expect((screen.getByLabelText("决策时点 as_of") as HTMLInputElement).value).toBe("2026-01-15T00:00:00Z");
    expect((screen.getByLabelText("决策时区") as HTMLInputElement).value).toBe("Asia/Tokyo");
    // The selects still show what was chosen, not their placeholder.
    const snapshotSelect = screen.getByLabelText("快照").closest(".ant-select") as HTMLElement;
    expect(within(snapshotSelect).getByText("快照一 · snap_1")).toBeTruthy();
    const universeSelect = screen.getByLabelText("标的池版本").closest(".ant-select") as HTMLElement;
    expect(within(universeSelect).getByText("测试池 · uni_1 · 静态")).toBeTruthy();
  });

  it("reaches the preflight from the keyboard alone", async () => {
    const user = userEvent.setup();
    renderPage();
    await fillRunForm();
    // Activating a focused button with Enter is the browser's own affordance; a
    // page that only worked on click would fail here.
    screen.getByRole("button", { name: /预\s*检/ }).focus();
    await user.keyboard("{Enter}");
    await waitFor(() => expect(calls.some((call) => call.url.includes("/screen-runs/preflight"))).toBe(true));
    await waitFor(() => expect(screen.getByText(/预检通过/)).toBeTruthy());
  });

  it("submits the run from a form field with Enter", async () => {
    const user = userEvent.setup();
    renderPage();
    await fillRunForm();
    const asOf = screen.getByLabelText("决策时点 as_of");
    asOf.focus();
    await user.keyboard("{Enter}");
    await waitFor(() => expect(calls.some((call) => call.url.endsWith("/screen-runs"))).toBe(true));
  });
});
