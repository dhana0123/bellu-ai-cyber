import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'

export default tseslint.config(
  { ignores: ['dist', 'node_modules'] },
  {
    extends: [js.configs.recommended, ...tseslint.configs.recommended],
    files: ['**/*.{ts,tsx}'],
    languageOptions: {
      ecmaVersion: 2022,
      globals: { ...globals.browser, ...globals.node },
    },
    plugins: {
      'react-hooks': reactHooks,
      'react-refresh': reactRefresh,
    },
    rules: {
      ...reactHooks.configs.recommended.rules,
      'react-refresh/only-export-components': ['warn', { allowConstantExport: true }],
      // The shadcn chart primitive legitimately uses `any` in a couple of
      // Recharts interop spots; keep as warnings, not errors.
      '@typescript-eslint/no-explicit-any': 'warn',
    },
  },
  // Config + test files run in Node and may use devDependencies freely.
  {
    files: ['**/*.config.{ts,js}', 'src/**/*.test.{ts,tsx}', 'src/test/**'],
    languageOptions: { globals: { ...globals.node } },
  },
)
