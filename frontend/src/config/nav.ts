import {
  Gauge,
  Boxes,
  GitBranch,
  Bot,
  Bug,
  ShieldAlert,
  Search,
  Radar,
  Share2,
  MessagesSquare,
  FlaskConical,
  Cloud,
  Users,
  FolderKanban,
  KeyRound,
  Settings,
  SlidersHorizontal,
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
      { to: '/ai-agents', label: 'AI Agents', icon: Bot, permission: 'graph:read' },
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
  { label: 'Investigate', icon: Search, to: '/investigate', permission: 'investigate' },
  {
    label: 'Runtime',
    icon: SlidersHorizontal,
    preview: true,
    children: [
      { to: '/sessions', label: 'Sessions', icon: MessagesSquare, permission: 'runtime:read' },
      { to: '/evaluations', label: 'Evaluations', icon: FlaskConical, permission: 'runtime:read' },
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
