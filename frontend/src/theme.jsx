// Migration shim: real implementation is in theme.tsx. Existing .jsx files
// import '../theme.jsx' with an explicit extension; this keeps them working
// until they migrate. Remove once no .jsx imports this path.
export { ThemeProvider, useTheme } from './theme.tsx'
