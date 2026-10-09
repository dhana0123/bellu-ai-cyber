import { Routes, Route, Navigate, useLocation, type ReactElement } from 'react-router-dom'
import { useAuth } from '@/auth'
import { Spinner } from '@/components/ui/spinner'
import { AppShell } from '@/components/common/AppShell'
import { ModulePlaceholder } from '@/components/common/ModulePlaceholder'

// Foundation pages are still JSX until Task 7 migrates them; import by path.
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

// RequireAuth gates a route: redirects to /login when unauthenticated and shows
// a 403 notice (inside the shell) when the user lacks the required permission.
function RequireAuth({
  permission,
  children,
}: {
  permission?: string
  children: ReactElement
}) {
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
      <AppShell>
        <div className="rounded-lg border border-destructive/30 bg-destructive/10 p-6 text-sm text-destructive">
          You don't have permission to view this page.
        </div>
      </AppShell>
    )
  }
  return <AppShell>{children}</AppShell>
}

const ADDON_MODULES: { path: string; perm: string; title: string; blurb: string }[] = [
  { path: '/gateway', perm: 'gateway:read', title: 'AI Gateway', blurb: 'Govern and route AI traffic.' },
  { path: '/runtime', perm: 'runtime:read', title: 'Runtime Defense', blurb: 'Live prompt/response protection.' },
  { path: '/agents', perm: 'agents:read', title: 'Agent Security', blurb: 'Inventory and control AI agents.' },
  { path: '/data', perm: 'data:read', title: 'AI Data Security', blurb: 'Track sensitive data in AI flows.' },
  {
    path: '/supply-chain',
    perm: 'supplychain:read',
    title: 'AI Supply Chain',
    blurb: 'Scan models and dependencies.',
  },
  { path: '/media', perm: 'media:read', title: 'Media Authenticity', blurb: 'Detect synthetic media.' },
  {
    path: '/governance',
    perm: 'governance:read',
    title: 'Governance & Compliance',
    blurb: 'Reports, policies and audit trails.',
  },
]

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />

      {/* AI-SPM foundation (real API) */}
      <Route path="/" element={<Navigate to="/overview" replace />} />
      <Route
        path="/overview"
        element={
          <RequireAuth permission="graph:read">
            <Dashboard />
          </RequireAuth>
        }
      />
      <Route
        path="/inventory"
        element={
          <RequireAuth permission="graph:read">
            <Entities />
          </RequireAuth>
        }
      />
      <Route
        path="/tracegraph"
        element={
          <RequireAuth permission="graph:read">
            <AttackPaths />
          </RequireAuth>
        }
      />
      <Route
        path="/findings"
        element={
          <RequireAuth permission="findings:read">
            <Findings />
          </RequireAuth>
        }
      />
      <Route
        path="/investigate"
        element={
          <RequireAuth permission="investigate">
            <Explain />
          </RequireAuth>
        }
      />

      {/* Add-ons (Preview, mock) — placeholders until Task 8 */}
      {ADDON_MODULES.map((m) => (
        <Route
          key={m.path}
          path={m.path}
          element={
            <RequireAuth permission={m.perm}>
              <ModulePlaceholder title={m.title} blurb={m.blurb} />
            </RequireAuth>
          }
        />
      ))}

      {/* Platform (real API) */}
      <Route
        path="/connectors"
        element={
          <RequireAuth permission="projects:read">
            <ConnectorsPage />
          </RequireAuth>
        }
      />
      <Route
        path="/connectors/new/aws"
        element={
          <RequireAuth permission="integrations:manage">
            <ConnectAwsPage />
          </RequireAuth>
        }
      />
      <Route
        path="/users"
        element={
          <RequireAuth permission="users:read">
            <UsersPage />
          </RequireAuth>
        }
      />
      <Route
        path="/projects"
        element={
          <RequireAuth permission="projects:read">
            <ProjectsPage />
          </RequireAuth>
        }
      />
      <Route
        path="/roles"
        element={
          <RequireAuth permission="users:read">
            <RolesPage />
          </RequireAuth>
        }
      />
      <Route
        path="/settings"
        element={
          <RequireAuth permission="projects:read">
            <ModulePlaceholder
              title="Settings"
              blurb="Platform configuration and preferences."
              preview={false}
            />
          </RequireAuth>
        }
      />

      {/* Legacy path redirects (pre-migration routes) */}
      <Route path="/entities" element={<Navigate to="/inventory" replace />} />
      <Route path="/attack-paths" element={<Navigate to="/tracegraph" replace />} />
      <Route path="/explain" element={<Navigate to="/investigate" replace />} />

      <Route path="*" element={<Navigate to="/overview" replace />} />
    </Routes>
  )
}
