// Migration shim: real implementation is in auth.tsx. Existing .jsx pages
// import '../auth.jsx' with an explicit extension; this keeps them working
// until they migrate. Remove once no .jsx imports this path.
export { AuthProvider, useAuth } from './auth.tsx'
