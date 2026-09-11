import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import App from "./App";

describe("App", () => {
  it("renders the scaffold placeholder", () => {
    render(<App />);
    expect(screen.getByRole("heading", { name: "策略研究工作台" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "尚未实现" })).toBeDisabled();
  });
});
