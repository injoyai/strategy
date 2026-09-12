import "@testing-library/jest-dom/vitest";

if (!("ResizeObserver" in globalThis)) {
  globalThis.ResizeObserver = class ResizeObserverMock {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
}
