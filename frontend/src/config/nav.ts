import {
  LayoutDashboard,
  Gauge,
  Boxes,
  ShieldAlert,
  FileWarning,
  Search,
  Users,
  FolderKanban,
  KeyRound,
  Cloud,
  Network,
  Radar,
  Bot,
  Database,
  Boxes as BoxesIcon,
  ScanFace,
  Scale,
  Settings,
  type LucideIcon,
} from 'lucide-react'

export interface NavItem {
  to: string
  label: string
  icon: LucideIcon
  /** Required permission; items the user lacks are hidden. */
  permission?: string
  /** Exact-match route (for the index route). */
  end?: boolean
}

export interface NavGroup {
  section: string
  /** Marks the whole section as a Preview (mock-data) add-on area. */
  preview?: boolean
  items: NavItem[]
}

// Config-driven sidebar. Each item is gated by an optional permission; items
// the current user lacks permission for are hidden automatically (Shell/
// AppShell filters). AI-SPM is the real-API foundation (TraceGraph lives inside
// it); the Add-ons section is Preview (mock data); Platform is real-API.
export const navConfig: NavGroup[] = [
  {
    section: 'AI-SPM',
    items: [
      { to: '/dashboard', label: 'Dashboard', icon: Gauge, permission: 'graph:read' },
      {
        to: '/overview',
        label: 'Security Overview',
        icon: LayoutDashboard,
        permission: 'graph:read',
      },
      { to: '/inventory', label: 'AI Inventory', icon: Boxes, permission: 'graph:read' },
      { to: '/tracegraph', label: 'TraceGraph', icon: ShieldAlert, permission: 'graph:read' },
      { to: '/findings', label: 'Findings', icon: FileWarning, permission: 'findings:read' },
      { to: '/issues', label: 'Risk Issues', icon: ShieldAlert, permission: 'findings:read' },
      { to: '/investigate', label: 'Investigate', icon: Search, permission: 'investigate' },
    ],
  },
  {
    section: 'Add-ons',
    preview: true,
    items: [
      { to: '/gateway', label: 'AI Gateway', icon: Network, permission: 'gateway:read' },
      { to: '/runtime', label: 'Runtime Defense', icon: Radar, permission: 'runtime:read' },
      { to: '/agents', label: 'Agent Security', icon: Bot, permission: 'agents:read' },
      { to: '/data', label: 'AI Data Security', icon: Database, permission: 'data:read' },
      {
        to: '/supply-chain',
        label: 'Supply Chain',
        icon: BoxesIcon,
        permission: 'supplychain:read',
      },
      { to: '/media', label: 'Media Authenticity', icon: ScanFace, permission: 'media:read' },
      { to: '/governance', label: 'Governance', icon: Scale, permission: 'governance:read' },
    ],
  },
  {
    section: 'Platform',
    items: [
      { to: '/connectors', label: 'Cloud Connectors', icon: Cloud, permission: 'projects:read' },
      { to: '/users', label: 'Users', icon: Users, permission: 'users:read' },
      { to: '/projects', label: 'Projects', icon: FolderKanban, permission: 'projects:read' },
      { to: '/roles', label: 'Roles', icon: KeyRound, permission: 'users:read' },
      { to: '/settings', label: 'Settings', icon: Settings, permission: 'projects:read' },
    ],
  },
]
