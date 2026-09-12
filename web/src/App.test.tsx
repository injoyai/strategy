import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";

describe("App", () => {
  beforeEach(() => {
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

  it("renders the M0 data workspace and navigable shell", async () => {
    render(<App />);
    expect(screen.getByRole("heading", { name: "数据中心" })).toBeInTheDocument();
    expect(screen.getByRole("navigation", { name: "工作区" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "数据源" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "发起数据更新" })).toBeInTheDocument();
  });
});
