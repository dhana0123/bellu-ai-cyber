import {
  LayoutDashboard,
  Boxes,
  ShieldAlert,
  Search,
  Users,
  FolderKanban,
  KeyRound,
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
      { to: '/explain', label: 'Investigate', icon: Search, permission: 'investigate' },
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
