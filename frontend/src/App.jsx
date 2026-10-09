import React from 'react'
import { Routes, Route, Navigate, useLocation } from 'react-router-dom'
import { useAuth } from './auth.jsx'
import { Spinner } from './components/ui/misc.jsx'
import Shell from './components/Shell.jsx'
import Login from './pages/Login.jsx'
import Dashboard from './pages/Dashboard.jsx'
import Entities from './pages/Entities.jsx'
import AttackPaths from './pages/AttackPaths.jsx'
import Findings from './pages/Findings.jsx'
import Explain from './pages/Explain.jsx'
import UsersPage from './pages/Users.jsx'
import ProjectsPage from './pages/Projects.jsx'
import RolesPage from './pages/Roles.jsx'
import ConnectorsPage from './pages/Connectors.jsx'
import ConnectAwsPage from './pages/ConnectAws.jsx'

// RequireAuth gates a route; redirects to /login when not authenticated and
// shows a 403 notice when the user lacks the required permission.
function RequireAuth({ permission, children }) {
  const { principal, loading, can } = useAuth()
  const location = useLocation()

  if (loading) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }
  if (!principal) {
    return <Navigate to="/login" replace state={{ from: location }} />
  }
  if (permission && !can(permission)) {
    return (
      <Shell>
        <div className="rounded-lg border border-destructive/30 bg-destructive/10 p-6 text-sm text-destructive">
          You don't have permission to view this page.
        </div>
      </Shell>
    )
  }
  return <Shell>{children}</Shell>
}

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route path="/" element={<RequireAuth permission="graph:read"><Dashboard /></RequireAuth>} />
      <Route path="/entities" element={<RequireAuth permission="graph:read"><Entities /></RequireAuth>} />
      <Route path="/attack-paths" element={<RequireAuth permission="graph:read"><AttackPaths /></RequireAuth>} />
      <Route path="/findings" element={<RequireAuth permission="findings:read"><Findings /></RequireAuth>} />
      <Route path="/explain" element={<RequireAuth permission="investigate"><Explain /></RequireAuth>} />
      <Route path="/users" element={<RequireAuth permission="users:read"><UsersPage /></RequireAuth>} />
      <Route path="/projects" element={<RequireAuth permission="projects:read"><ProjectsPage /></RequireAuth>} />
      <Route path="/roles" element={<RequireAuth permission="users:read"><RolesPage /></RequireAuth>} />
      <Route path="/connectors" element={<RequireAuth permission="projects:read"><ConnectorsPage /></RequireAuth>} />
      <Route path="/connectors/new/aws" element={<RequireAuth permission="integrations:manage"><ConnectAwsPage /></RequireAuth>} />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}
