import {
  Gauge,
  Boxes,
  GitBranch,
  Bug,
  ShieldAlert,
  Radar,
  Activity,
  Share2,
  Cloud,
  Users,
  FolderKanban,
  KeyRound,
  Settings,
  type LucideIcon,
} from 'lucide-react'

// A leaf navigation link.
export interface NavLeaf {
  to: string
  label: string
  icon: LucideIcon
  /** Required permission; items the user lacks are hidden. */
  permission?: string
  end?: boolean
}

// A primary sidebar entry. Either a direct link (has `to`) or a flyout parent
// (has `children`). `preview` marks a mock-data (Preview) area.
export interface NavNode {
  label: string
  icon: LucideIcon
  to?: string
  end?: boolean
  permission?: string
  preview?: boolean
  children?: NavLeaf[]
}

// The sidebar tree. Primary items with `children` render a hover flyout; items
// with only `to` are direct links. Permission gating is applied per leaf (and
// a parent is hidden when all its children are gated out).
export const navTree: NavNode[] = [
  { label: 'Dashboard', icon: Gauge, to: '/dashboard', permission: 'graph:read' },
  {
    label: 'Discovery',
    icon: Radar,
    children: [
      { to: '/explorer', label: 'Knowledge Graph', icon: Share2, permission: 'graph:read' },
      { to: '/inventory', label: 'Inventory', icon: Boxes, permission: 'graph:read' },
      { to: '/repositories', label: 'Repositories', icon: GitBranch, permission: 'graph:read' },
    ],
  },
  {
    label: 'Dangerous',
    icon: ShieldAlert,
    children: [
      { to: '/vulnerabilities', label: 'Vulnerabilities', icon: Bug, permission: 'findings:read' },
      { to: '/issues', label: 'Issues', icon: ShieldAlert, permission: 'findings:read' },
    ],
  },
  {
    label: 'Monitoring',
    icon: Radar,
    children: [
      {
        to: '/mission-control',
        label: 'Mission Control',
        icon: Activity,
        permission: 'findings:read',
      },
    ],
  },
  {
    label: 'Platform',
    icon: Cloud,
    children: [
      { to: '/connectors', label: 'Cloud Connectors', icon: Cloud, permission: 'projects:read' },
      { to: '/users', label: 'Users', icon: Users, permission: 'users:read' },
      { to: '/projects', label: 'Projects', icon: FolderKanban, permission: 'projects:read' },
      { to: '/roles', label: 'Roles', icon: KeyRound, permission: 'users:read' },
      { to: '/settings', label: 'Settings', icon: Settings, permission: 'projects:read' },
    ],
  },
]
