import '@testing-library/jest-dom/vitest'

// jsdom lacks ResizeObserver, which Recharts' ResponsiveContainer requires.
// Provide a no-op polyfill so chart-containing components render in tests.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver = globalThis.ResizeObserver ?? (ResizeObserverStub as never)
