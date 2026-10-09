import {
  LayoutDashboard,
  Boxes,
  ShieldAlert,
  FileWarning,
  Search,
  Users,
  FolderKanban,
  KeyRound,
  Cloud,
} from 'lucide-react'

// Config-driven sidebar. Each item is gated by an optional required permission;
// items the current user lacks permission for are hidden automatically.
// `section` groups items under a heading.
export const navConfig = [
  {
    section: 'Security Graph',
    items: [
      { to: '/', label: 'Dashboard', icon: LayoutDashboard, permission: 'graph:read', end: true },
      { to: '/entities', label: 'Entities', icon: Boxes, permission: 'graph:read' },
      { to: '/attack-paths', label: 'Attack Paths', icon: ShieldAlert, permission: 'graph:read' },
      { to: '/findings', label: 'Findings', icon: FileWarning, permission: 'findings:read' },
      { to: '/explain', label: 'Investigate', icon: Search, permission: 'investigate' },
    ],
  },
  {
    section: 'Integrations',
    items: [
      { to: '/connectors', label: 'Cloud Connectors', icon: Cloud, permission: 'projects:read' },
    ],
  },
  {
    section: 'Administration',
    items: [
      { to: '/users', label: 'Users', icon: Users, permission: 'users:read' },
      { to: '/projects', label: 'Projects', icon: FolderKanban, permission: 'projects:read' },
      { to: '/roles', label: 'Roles', icon: KeyRound, permission: 'users:read' },
    ],
  },
]
