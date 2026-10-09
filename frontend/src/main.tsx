import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import App from './App'
import { ThemeProvider } from './theme'
import { AuthProvider } from './auth'
import { Toaster } from '@/components/ui/sonner'
import './styles.css'

// Provider nesting preserved from the pre-migration app: Theme (independent of
// auth) → Router (so auth can navigate) → Auth (so routes see the principal).
ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <ThemeProvider defaultTheme="dark">
      <BrowserRouter>
        <AuthProvider>
          <App />
          <Toaster />
        </AuthProvider>
      </BrowserRouter>
    </ThemeProvider>
  </React.StrictMode>,
)
