import type { ReactElement } from 'react'
import { Routes, Route, Navigate, useLocation } from 'react-router-dom'
import { useAuth } from '@/auth'
import { Spinner } from '@/components/ui/spinner'
import { AppShell } from '@/components/common/AppShell'
import { ModulePlaceholder } from '@/components/common/ModulePlaceholder'

// Migrated AI-SPM foundation pages (TypeScript + shadcn + charts).
import Login from '@/features/ai-spm/Login'
import Dashboard from '@/features/dashboard/Dashboard'
import Issues from '@/features/issues/Issues'
import SecurityOverview from '@/features/ai-spm/SecurityOverview'
import AiInventory from '@/features/ai-spm/AiInventory'
import AiEcosystemInventory from '@/features/ai-inventory/AiEcosystemInventory'
import TraceGraphPage from '@/features/ai-spm/TraceGraphPage'
import Findings from '@/features/ai-spm/Findings'
import Vulnerabilities from '@/features/vulnerabilities/Vulnerabilities'
import Investigate from '@/features/ai-spm/Investigate'
// Add-on modules (Preview, mock-data).
import Gateway from '@/features/gateway/Gateway'
import Runtime from '@/features/runtime/Runtime'
import Agents from '@/features/agents/Agents'
import DataSecurity from '@/features/data/DataSecurity'
import SupplyChain from '@/features/supply-chain/SupplyChain'
import Media from '@/features/media/Media'
import Governance from '@/features/governance/Governance'
// Platform pages (TypeScript + shadcn).
import UsersPage from '@/features/platform/Users'
import ProjectsPage from '@/features/platform/Projects'
import RolesPage from '@/features/platform/Roles'
import ConnectorsPage from '@/features/platform/Connectors'
import ConnectAwsPage from '@/features/platform/ConnectAws'
import ConnectProviderPage from '@/features/platform/ConnectProvider'
import RepositoriesPage from '@/features/platform/Repositories'

// RequireAuth gates a route: redirects to /login when unauthenticated and shows
// a 403 notice (inside the shell) when the user lacks the required permission.
function RequireAuth({ permission, children }: { permission?: string; children: ReactElement }) {
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

// Add-on (Preview) modules, each a mock-backed feature gated by its proposed
// permission string.
const ADDON_MODULES: { path: string; perm: string; element: ReactElement }[] = [
  { path: '/gateway', perm: 'gateway:read', element: <Gateway /> },
  { path: '/runtime', perm: 'runtime:read', element: <Runtime /> },
  { path: '/agents', perm: 'agents:read', element: <Agents /> },
  { path: '/data', perm: 'data:read', element: <DataSecurity /> },
  { path: '/supply-chain', perm: 'supplychain:read', element: <SupplyChain /> },
  { path: '/media', perm: 'media:read', element: <Media /> },
  { path: '/governance', perm: 'governance:read', element: <Governance /> },
]

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />

      {/* AI-SPM foundation (real API) */}
      <Route path="/" element={<Navigate to="/dashboard" replace />} />
      <Route
        path="/dashboard"
        element={
          <RequireAuth permission="graph:read">
            <Dashboard />
          </RequireAuth>
        }
      />
      <Route
        path="/overview"
        element={
          <RequireAuth permission="graph:read">
            <SecurityOverview />
          </RequireAuth>
        }
      />
      <Route
        path="/inventory"
        element={
          <RequireAuth permission="graph:read">
            <AiEcosystemInventory />
          </RequireAuth>
        }
      />
      <Route
        path="/inventory-graph"
        element={
          <RequireAuth permission="graph:read">
            <AiInventory />
          </RequireAuth>
        }
      />
      <Route
        path="/tracegraph"
        element={
          <RequireAuth permission="graph:read">
            <TraceGraphPage />
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
        path="/vulnerabilities"
        element={
          <RequireAuth permission="findings:read">
            <Vulnerabilities />
          </RequireAuth>
        }
      />
      <Route
        path="/issues"
        element={
          <RequireAuth permission="findings:read">
            <Issues />
          </RequireAuth>
        }
      />

      {/* Discovery / Runtime nav items without a full page yet — placeholders. */}
      <Route
        path="/repositories"
        element={
          <RequireAuth permission="graph:read">
            <RepositoriesPage />
          </RequireAuth>
        }
      />
      <Route
        path="/ai-agents"
        element={
          <RequireAuth permission="graph:read">
            <ModulePlaceholder
              title="AI Agents"
              blurb="Discovered AI agents across your environments."
              preview={false}
            />
          </RequireAuth>
        }
      />
      <Route
        path="/sessions"
        element={
          <RequireAuth permission="runtime:read">
            <ModulePlaceholder title="Sessions" blurb="Live and historical AI runtime sessions." />
          </RequireAuth>
        }
      />
      <Route
        path="/evaluations"
        element={
          <RequireAuth permission="runtime:read">
            <ModulePlaceholder
              title="Evaluations"
              blurb="Model and agent evaluation runs and scorecards."
            />
          </RequireAuth>
        }
      />
      <Route
        path="/investigate"
        element={
          <RequireAuth permission="investigate">
            <Investigate />
          </RequireAuth>
        }
      />

      {/* Add-ons (Preview, mock) — placeholders until Task 8 */}
      {ADDON_MODULES.map((m) => (
        <Route
          key={m.path}
          path={m.path}
          element={<RequireAuth permission={m.perm}>{m.element}</RequireAuth>}
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
      {/* Generic onboarding for non-AWS providers (azure, gcp, github, gitlab,
          atlassian). AWS keeps its specialized wizard above. */}
      <Route
        path="/connectors/new/:provider"
        element={
          <RequireAuth permission="integrations:manage">
            <ConnectProviderPage />
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

      <Route path="*" element={<Navigate to="/dashboard" replace />} />
    </Routes>
  )
}
