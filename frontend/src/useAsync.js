// Migration shim: real implementation is in useAsync.ts. Existing .jsx pages
// import '../useAsync.js' with an explicit extension; this keeps them working
// until they migrate to TS. Remove once no .jsx imports this path.
export { useAsync } from './useAsync.ts'
