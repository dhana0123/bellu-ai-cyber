// Shared mock helpers. All add-on data served through these is DEMO data — it
// is never real customer telemetry.

/** Resolve `value` after a short simulated network delay (default 300ms). */
export function withLatency<T>(value: T, ms = 300): Promise<T> {
  return new Promise((resolve) => setTimeout(() => resolve(value), ms))
}

/** Marker so it's always obvious in code and tooling that data is demo-only. */
export const DEMO_NOTICE = 'DEMO data — not real customer telemetry.' as const
