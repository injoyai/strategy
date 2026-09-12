import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";

describe("App", () => {
  beforeEach(() => {
    window.history.pushState({}, "", "/data");
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = input instanceof Request ? input.url : String(input);
      if (url.includes("/datasets")) return Response.json({ items: [], next_cursor: null });
      if (url.includes("/connections")) return Response.json({ items: [], next_cursor: null });
      if (url.includes("/batches")) return Response.json({ items: [], next_cursor: null });
      if (url.includes("/snapshots")) return Response.json({ items: [], next_cursor: null });
      if (url.includes("/jobs")) return Response.json({ items: [], next_cursor: null });
      if (url.includes("/providers")) return Response.json({ items: [], next_cursor: null });
      return Response.json({});
    }));
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("renders the M0 data workspace and navigable shell", async () => {
    render(<App />);
    expect(screen.getByRole("heading", { name: "数据中心" })).toBeInTheDocument();
    expect(screen.getByRole("navigation", { name: "工作区" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "数据源" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "发起数据更新" })).toBeInTheDocument();
  });

  it("navigates across the M0 routes and updates the document title", async () => {
    render(<App />);

    fireEvent.click(screen.getByRole("link", { name: "数据源" }));
    expect(await screen.findByRole("heading", { name: "数据源" })).toBeInTheDocument();
    expect(document.title).toBe("数据源 — 策略研究工作台");

    fireEvent.click(screen.getByRole("link", { name: "任务中心" }));
    expect(await screen.findByRole("heading", { name: "任务中心" })).toBeInTheDocument();
    expect(document.title).toBe("任务中心 — 策略研究工作台");
  });

  it("shows a retryable service error when the API is unavailable", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => {
      throw new TypeError("Failed to fetch");
    }));
    render(<App />);

    expect(await screen.findAllByText("研究服务暂时不可用，请稍后重试。")).toHaveLength(4);
  });
});
