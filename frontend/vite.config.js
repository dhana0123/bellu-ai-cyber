import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The frontend talks to the Go control-panel API. In dev, /api is proxied to
// the backend so there are no CORS issues; override with VITE_API_TARGET.
const apiTarget = process.env.VITE_API_TARGET || 'http://localhost:8080'

export default defineConfig({
  plugins: [react()],
  server: {
    host: true,
    port: 5173,
    proxy: {
      '/api': { target: apiTarget, changeOrigin: true },
    },
  },
})
