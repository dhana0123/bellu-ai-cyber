// Migration shim: real implementation is in nav.ts. Kept so the legacy
// Shell.jsx (if still referenced) resolves. Remove once nothing .jsx imports it.
export { navConfig } from './nav.ts'
