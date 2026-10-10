// Typed access to Vite env + a trivial import to prove the `@/` alias and the
// TypeScript toolchain compile alongside the existing JSX during migration.
// Safe to keep: foundation code will import API_BASE from here.

/** Base URL for API requests. Empty string = same-origin (dev proxy / nginx). */
export const API_BASE: string = import.meta.env.VITE_API_BASE ?? ''

/** True in `vite dev`, false in a production build. */
export const IS_DEV: boolean = import.meta.env.DEV

/**
 * Reveal the Preview (mock-data) add-on modules even in a production build.
 * Set `VITE_SHOW_PREVIEW=true` at build time (e.g. for a dev/demo container).
 * Off by default so real deployments keep add-ons gated behind real backend
 * permissions. Mock data stays clearly labeled "Preview — demo data".
 */
export const SHOW_PREVIEW: boolean = IS_DEV || import.meta.env.VITE_SHOW_PREVIEW === 'true'
