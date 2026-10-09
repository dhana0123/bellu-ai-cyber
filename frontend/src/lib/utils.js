// Compatibility shim during the TS migration: the real implementation lives in
// utils.ts. Existing .jsx files import `../../lib/utils.js` with an explicit
// extension; this re-export keeps them working until each is migrated to TS and
// switched to `@/lib/utils`. Remove once no .jsx imports this path.
export { cn } from './utils.ts'
