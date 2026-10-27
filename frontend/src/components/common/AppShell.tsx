import { useEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { NavLink, useNavigate, useLocation } from 'react-router-dom'
import { ShieldCheck, Moon, Sun, LogOut, X } from 'lucide-react'
import { navTree, type NavNode } from '@/config/nav'
import { useAuth } from '@/auth'
import { useTheme } from '@/theme'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarInset,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarProvider,
  SidebarRail,
  SidebarTrigger,
} from '@/components/ui/sidebar'
import { ProjectSwitcher } from '@/components/common/ProjectSwitcher'

// AppShell renders the standard shadcn sidebar: an expanded, labeled sidebar
// grouped into sections, with a built-in collapse/expand trigger (the
// "minimizer") that shrinks it to an icon rail and back. The top header exposes
// a portal slot ("app-header-slot") that pages fill with their own controls.

const HEADER_SLOT_ID = 'app-header-slot'

// HeaderSlot: pages render into the shell's top header via this portal.
export function HeaderSlot({ children }: { children: ReactNode }) {
  const [el, setEl] = useState<HTMLElement | null>(null)
  useEffect(() => {
    setEl(document.getElementById(HEADER_SLOT_ID))
  }, [])
  if (!el) return null
  return createPortal(children, el)
}

// Permission-filter the nav tree: drop leaves the user can't access, and drop a
// parent with no surviving children (or a direct link they can't access).
function visibleNodes(can: (perm: string) => boolean): NavNode[] {
  return navTree
    .map((n): NavNode | null => {
      if (n.children) {
        const kids = n.children.filter((c) => !c.permission || can(c.permission))
        return kids.length ? { ...n, children: kids } : null
      }
      if (n.permission && !can(n.permission)) return null
      return n
    })
    .filter((n): n is NavNode => n != null)
}

// A labeled sidebar link. Uses SidebarMenuButton (which handles the collapsed
// icon-only state + tooltip) wrapping a NavLink for active styling + routing.
function NavItem({ to, label, Icon, end }: { to: string; label: string; Icon: NavNode['icon']; end?: boolean }) {
  return (
    <SidebarMenuItem>
      <NavLink to={to} end={end}>
        {({ isActive }) => (
          <SidebarMenuButton isActive={isActive} tooltip={label}>
            <Icon />
            <span>{label}</span>
          </SidebarMenuButton>
        )}
      </NavLink>
    </SidebarMenuItem>
  )
}

// DrawerNav renders a parent node as a SINGLE sidebar item. Clicking it toggles
// a FIXED submenu panel (PlatformPanel) rendered inline next to the sidebar
// (not a sliding overlay). The open state is owned by AppShell.
function DrawerNav({
  node,
  active,
  onToggle,
}: {
  node: NavNode
  active: boolean
  onToggle: () => void
}) {
  const Icon = node.icon
  return (
    <SidebarMenuItem>
      <SidebarMenuButton tooltip={node.label} isActive={active} onClick={onToggle}>
        <Icon />
        <span>{node.label}</span>
      </SidebarMenuButton>
    </SidebarMenuItem>
  )
}

// PlatformPanel is the FIXED, full-height submenu column that sits inline
// between the main sidebar and the page content (it pushes content rather than
// overlaying it). It lists the drawer node's children as links; selecting one
// navigates but keeps the panel open so the operator can move between views.
function PlatformPanel({ node, onClose }: { node: NavNode; onClose: () => void }) {
  const Icon = node.icon
  return (
    <aside className="flex h-svh w-64 shrink-0 flex-col border-r bg-sidebar">
      <div className="flex items-center justify-between border-b p-4">
        <div className="flex items-center gap-2">
          <Icon className="h-4 w-4" />
          <span className="text-sm font-semibold">{node.label}</span>
        </div>
        <Button variant="ghost" size="icon" className="h-7 w-7" onClick={onClose} aria-label="Close panel">
          <X className="h-4 w-4" />
        </Button>
      </div>
      <nav className="flex-1 overflow-y-auto p-2">
        <ul className="space-y-1">
          {node.children?.map((c) => {
            const ChildIcon = c.icon
            return (
              <li key={c.to}>
                <NavLink to={c.to} end={c.end}>
                  {({ isActive }) => (
                    <span
                      className={cn(
                        'flex items-center gap-3 rounded-md px-3 py-2 text-sm transition-colors',
                        isActive
                          ? 'bg-accent font-medium text-foreground'
                          : 'text-muted-foreground hover:bg-accent/60 hover:text-foreground',
                      )}
                    >
                      <ChildIcon className="h-4 w-4 shrink-0" />
                      <span className="truncate">{c.label}</span>
                    </span>
                  )}
                </NavLink>
              </li>
            )
          })}
        </ul>
      </nav>
    </aside>
  )
}

// A rendered sidebar section: either a run of adjacent link/drawer items that
// share one group (no label), or a single labeled group that has children.
type NavSection =
  | { kind: 'links'; items: NavNode[] }
  | { kind: 'group'; node: NavNode }

// A node renders as a single row (a direct link, or a drawer toggle) when it is
// NOT an inline group-with-children. These are merged into one shared group so
// consecutive top-level items (e.g. Dashboard, Mission Control) don't stack
// group paddings and leave a big gap between them.
function isLinkLike(n: NavNode): boolean {
  return n.drawer === true || !n.children
}

// Collapse the flat nav list into sections: runs of adjacent link-like nodes
// become one 'links' section; each inline group-with-children stays its own
// labeled 'group' section, preserving order.
function mergeTopLevel(nodes: NavNode[]): NavSection[] {
  const out: NavSection[] = []
  for (const n of nodes) {
    if (isLinkLike(n)) {
      const last = out[out.length - 1]
      if (last && last.kind === 'links') last.items.push(n)
      else out.push({ kind: 'links', items: [n] })
    } else {
      out.push({ kind: 'group', node: n })
    }
  }
  return out
}

function AppSidebar({
  can,
  openDrawer,
  onToggleDrawer,
}: {
  can: (perm: string) => boolean
  openDrawer: string | null
  onToggleDrawer: (label: string) => void
}) {
  const nodes = visibleNodes(can)
  return (
    <Sidebar collapsible="icon" className="cg-sidebar">
      <SidebarHeader>
        <div className="flex h-8 items-center gap-2 px-1">
          {/* Brand mark: Bellu Violet → Logo Teal gradient disc. */}
          <div
            className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full text-white shadow-sm"
            style={{ backgroundImage: 'linear-gradient(135deg, #8B5CF6 0%, #2BC8B7 100%)' }}
          >
            <ShieldCheck className="h-4 w-4" />
          </div>
          <span className="truncate bg-gradient-to-r from-[#8B5CF6] to-[#2BC8B7] bg-clip-text text-base font-semibold tracking-tight text-transparent group-data-[collapsible=icon]:hidden">
            Bellu
          </span>
        </div>
      </SidebarHeader>

      <SidebarContent>
        {mergeTopLevel(nodes).map((section, i) =>
          // A "links" section is one or more adjacent top-level items that are
          // direct links or drawer toggles (no children). They share ONE group +
          // menu so they sit as tight, adjacent rows — no doubled group padding
          // between e.g. Dashboard and Mission Control.
          section.kind === 'links' ? (
            <SidebarGroup key={`links-${i}`}>
              <SidebarMenu>
                {section.items.map((n) =>
                  n.drawer && n.children ? (
                    <DrawerNav
                      key={n.label}
                      node={n}
                      active={openDrawer === n.label}
                      onToggle={() => onToggleDrawer(n.label)}
                    />
                  ) : (
                    n.to && (
                      <NavItem
                        key={n.to}
                        to={n.to}
                        label={n.label}
                        Icon={n.icon}
                        end={n.end}
                      />
                    )
                  ),
                )}
              </SidebarMenu>
            </SidebarGroup>
          ) : (
            // A labeled group with children.
            <SidebarGroup key={section.node.label}>
              <SidebarGroupLabel>
                {section.node.label}
                {section.node.preview ? (
                  <Badge variant="warning" className="ml-1.5 px-1.5 py-0 text-[10px]">
                    Preview
                  </Badge>
                ) : null}
              </SidebarGroupLabel>
              <SidebarMenu>
                {section.node.children?.map((c) => (
                  <NavItem key={c.to} to={c.to} label={c.label} Icon={c.icon} end={c.end} />
                ))}
              </SidebarMenu>
            </SidebarGroup>
          ),
        )}
      </SidebarContent>

      <SidebarFooter>
        <AccountFooter />
      </SidebarFooter>

      <SidebarRail />
    </Sidebar>
  )
}

// Account footer: user identity + theme toggle + sign-out. Collapses gracefully
// to icons when the sidebar is minimized.
function AccountFooter() {
  const { principal, logout } = useAuth()
  const { theme, setTheme } = useTheme()
  const navigate = useNavigate()
  const topRole = principal?.memberships?.[0]?.role_name || 'User'
  const email = principal?.user?.email

  const toggleTheme = () => setTheme(theme === 'dark' ? 'light' : 'dark')
  const signOut = () => {
    logout()
    navigate('/login', { replace: true })
  }

  return (
    <div className="space-y-1">
      <div className="flex items-center gap-2 px-1 py-1">
        <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-primary text-xs font-semibold text-primary-foreground">
          {(email?.[0] ?? 'U').toUpperCase()}
        </div>
        <div className="min-w-0 flex-1 group-data-[collapsible=icon]:hidden">
          <div className="truncate text-sm font-medium">{email ?? 'User'}</div>
          <div className="truncate text-xs text-muted-foreground">{topRole}</div>
        </div>
      </div>
      <div className="flex items-center gap-1 group-data-[collapsible=icon]:flex-col">
        <Button
          variant="ghost"
          size="sm"
          className="flex-1 justify-start gap-2 text-muted-foreground group-data-[collapsible=icon]:w-8 group-data-[collapsible=icon]:flex-none group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:px-0"
          onClick={toggleTheme}
          title={theme === 'dark' ? 'Light mode' : 'Dark mode'}
        >
          {theme === 'dark' ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
          <span className="group-data-[collapsible=icon]:hidden">
            {theme === 'dark' ? 'Light' : 'Dark'}
          </span>
        </Button>
        <Button
          variant="ghost"
          size="sm"
          className="flex-1 justify-start gap-2 text-muted-foreground group-data-[collapsible=icon]:w-8 group-data-[collapsible=icon]:flex-none group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:px-0"
          onClick={signOut}
          title="Sign out"
        >
          <LogOut className="h-4 w-4" />
          <span className="group-data-[collapsible=icon]:hidden">Sign out</span>
        </Button>
      </div>
    </div>
  )
}

export function AppShell({ children }: { children: ReactNode }) {
  const { can } = useAuth()
  const navigate = useNavigate()
  const location = useLocation()
  // Which drawer node (by label) has its fixed submenu panel open, or null.
  const [openDrawer, setOpenDrawer] = useState<string | null>(null)

  const nodes = visibleNodes(can)

  // When the panel opens we navigate to its first sub-item; that navigation
  // would otherwise immediately trip the auto-close effect below (the path
  // changes before the effect sees the new drawer). This flag tells the effect
  // to skip exactly one run: the one caused by opening the panel.
  const justOpened = useRef(false)

  // Does `pathname` belong to the given drawer node's section? True when it
  // matches a child link exactly OR is a sub-route of one (e.g. /connectors/new
  // /aws is under the /connectors child), so deep flows (connect/scan/edit) keep
  // the panel open instead of closing it.
  const pathInNode = (node: NavNode, pathname: string): boolean =>
    node.children?.some(
      (c) => pathname === c.to || pathname.startsWith(`${c.to}/`),
    ) ?? false

  // Toggle a drawer node. Opening it defaults the selection to its first
  // sub-item (e.g. Platform -> Cloud Connectors), matching typical drawer UX.
  const toggleDrawer = (label: string) => {
    setOpenDrawer((cur) => {
      if (cur === label) return null // clicking the open drawer closes it
      const node = nodes.find((n) => n.label === label && n.drawer && n.children)
      const first = node?.children?.[0]
      // Only auto-navigate if we're not already within its section.
      if (first && node && !pathInNode(node, location.pathname)) {
        justOpened.current = true
        navigate(first.to)
      }
      return label
    })
  }

  // Resolve the currently-open drawer node from the permission-filtered tree.
  const drawerNode =
    openDrawer != null
      ? nodes.find((n) => n.label === openDrawer && n.drawer && n.children) ?? null
      : null

  // Auto-close the panel when navigation lands outside the open drawer's
  // sub-items — i.e. the operator chose any menu option outside the panel.
  // Skip the single navigation caused by opening the panel itself.
  useEffect(() => {
    if (!drawerNode) return
    if (justOpened.current) {
      justOpened.current = false
      return
    }
    // Stay open for the whole section (child links and their sub-routes, e.g.
    // the connector connect/edit flows under /connectors). Close only when the
    // user navigates somewhere outside the section.
    if (!pathInNode(drawerNode, location.pathname)) setOpenDrawer(null)
  }, [location.pathname, drawerNode])

  return (
    // h-svh + overflow-hidden caps the shell to exactly one viewport so the
    // content area (<main>, flex-1) is bounded and scrolls INTERNALLY instead
    // of growing the page past the viewport. min-h-0 lets the inset column
    // shrink so a full-height page (e.g. the graph explorer) fits precisely.
    <SidebarProvider className="h-svh overflow-hidden">
      <AppSidebar can={can} openDrawer={openDrawer} onToggleDrawer={toggleDrawer} />
      {/* Fixed inline submenu panel (not an overlay): pushes the content. */}
      {drawerNode && (
        <PlatformPanel node={drawerNode} onClose={() => setOpenDrawer(null)} />
      )}
      <SidebarInset className="min-h-0">
        <header className="flex h-14 shrink-0 items-center gap-3 border-b px-4 md:px-6">
          {/* The minimizer: collapses / expands the sidebar. */}
          <SidebarTrigger className="-ml-1" />

          <div className="h-6 w-px bg-border" />

          {/* Global project switcher: active project for all project-scoped views. */}
          <ProjectSwitcher />

          <div className="h-6 w-px bg-border" />

          {/* Page-provided header content renders here via <HeaderSlot>. */}
          <div id={HEADER_SLOT_ID} className="flex min-w-0 flex-1 items-center gap-3" />
        </header>

        <main className="flex-1 overflow-y-auto p-4 md:p-6">{children}</main>
      </SidebarInset>
    </SidebarProvider>
  )
}
