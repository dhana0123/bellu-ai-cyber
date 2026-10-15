import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import App from './App'
import { ThemeProvider } from './theme'
import { AuthProvider } from './auth'
import { ProjectProvider } from './project'
import { Toaster } from '@/components/ui/sonner'
import './styles.css'

// Provider nesting preserved from the pre-migration app: Theme (independent of
// auth) → Router (so auth can navigate) → Auth (so routes see the principal) →
// Project (reads the principal to load the org's projects / active project).
ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <ThemeProvider defaultTheme="dark">
      <BrowserRouter>
        <AuthProvider>
          <ProjectProvider>
            <App />
            <Toaster />
          </ProjectProvider>
        </AuthProvider>
      </BrowserRouter>
    </ThemeProvider>
  </React.StrictMode>,
)
