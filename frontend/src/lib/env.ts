// Typed access to Vite env + a trivial import to prove the `@/` alias and the
// TypeScript toolchain compile alongside the existing JSX during migration.
// Safe to keep: foundation code will import API_BASE from here.

/** Base URL for API requests. Empty string = same-origin (dev proxy / nginx). */
export const API_BASE: string = import.meta.env.VITE_API_BASE ?? ''

/** True in `vite dev`, false in a production build. */
export const IS_DEV: boolean = import.meta.env.DEV
