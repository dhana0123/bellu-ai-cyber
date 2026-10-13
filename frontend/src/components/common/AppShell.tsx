import { useEffect, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { NavLink, useLocation, useNavigate } from 'react-router-dom'
import { ShieldCheck, Moon, Sun, LogOut, Menu } from 'lucide-react'
import { navTree, type NavNode } from '@/config/nav'
import { useAuth } from '@/auth'
import { useTheme } from '@/theme'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Sheet, SheetContent, SheetTrigger } from '@/components/ui/sheet'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'

// AppShell renders a Wiz-style two-level sidebar: a thin icon RAIL on the far
// left, and — when the pointer is over a primary item that has sub-items — a
// full-height SUB-PANEL that slides out immediately to the right of the rail
// (overlaying the content, z-50) listing that section's links. Direct-link
// items (Dashboard, Investigate) just navigate. The top header exposes a portal
// slot ("app-header-slot") that pages fill with their own title/controls.

const HEADER_SLOT_ID = 'app-header-slot'
const RAIL_W = 'w-[72px]'

// HeaderSlot: pages render into the shell's top header via this portal.
export function HeaderSlot({ children }: { children: ReactNode }) {
  const [el, setEl] = useState<HTMLElement | null>(null)
  useEffect(() => {
    setEl(document.getElementById(HEADER_SLOT_ID))
  }, [])
  if (!el) return null
  return createPortal(children, el)
}

// A primary rail item: a compact icon + label button. Direct-link nodes render
// a NavLink; parent nodes render a hover trigger whose full-height sub-panel is
// rendered as a sibling so the hover spans both (group/rail).
function RailItem({
  node,
  onNavigate,
}: {
  node: NavNode
  onNavigate?: () => void
}) {
  const location = useLocation()
  const children = node.children ?? []
  const isParent = children.length > 0
  const childActive = children.some((c) => location.pathname.startsWith(c.to))

  const railBtn = (active: boolean) =>
    cn(
      'flex w-full flex-col items-center gap-1 rounded-lg px-1 py-2 text-[10px] font-medium transition-colors',
      active
        ? 'bg-accent text-foreground'
        : 'text-muted-foreground hover:bg-accent hover:text-foreground',
    )

  // Direct link (Dashboard, Investigate).
  if (!isParent && node.to) {
    return (
      <NavLink
        to={node.to}
        end={node.end}
        onClick={onNavigate}
        className={({ isActive }) => railBtn(isActive)}
      >
        <node.icon className="h-5 w-5" />
        <span className="w-full truncate text-center leading-tight">{node.label}</span>
      </NavLink>
    )
  }

  // Parent with a full-height sub-panel.
  return (
    <div className="group/rail static">
      <button type="button" className={railBtn(childActive)} aria-haspopup="menu">
        <node.icon className="h-5 w-5" />
        <span className="w-full truncate text-center leading-tight">{node.label}</span>
      </button>

      {/* Full-height sub-panel: fixed to the rail's right edge, overlays content. */}
      <div
        className={cn(
          'invisible fixed bottom-0 top-0 z-50 w-60 border-r bg-card opacity-0 shadow-xl transition-all duration-200 ease-in-out',
          'group-hover/rail:visible group-hover/rail:opacity-100',
        )}
        style={{ left: 72 }}
      >
        <div className="flex h-14 items-center gap-2 border-b px-4">
          <node.icon className="h-4 w-4 text-muted-foreground" />
          <span className="text-sm font-semibold">{node.label}</span>
          {node.preview ? (
            <Badge variant="warning" className="px-1.5 py-0 text-[10px]">
              Preview
            </Badge>
          ) : null}
        </div>
        <div className="space-y-0.5 p-2">
          {children.map((c) => (
            <NavLink
              key={c.to}
              to={c.to}
              end={c.end}
              onClick={onNavigate}
              className={({ isActive }) =>
                cn(
                  'flex items-center gap-3 rounded-md px-3 py-2 text-sm transition-colors',
                  isActive
                    ? 'bg-accent font-medium text-foreground'
                    : 'font-normal text-muted-foreground hover:bg-accent hover:text-foreground',
                )
              }
            >
              <c.icon className="h-4 w-4 shrink-0" />
              <span className="truncate">{c.label}</span>
            </NavLink>
          ))}
        </div>
      </div>
    </div>
  )
}

function RailNav({ can, onNavigate }: { can: (perm: string) => boolean; onNavigate?: () => void }) {
  // Filter leaves by permission; drop a parent with no surviving children and a
  // direct link the user can't access.
  const nodes = navTree
    .map((n): NavNode | null => {
      if (n.children) {
        const kids = n.children.filter((c) => !c.permission || can(c.permission))
        return kids.length ? { ...n, children: kids } : null
      }
      if (n.permission && !can(n.permission)) return null
      return n
    })
    .filter((n): n is NavNode => n != null)

  return (
    <nav className="sidebar-rail-scroll flex-1 space-y-1 overflow-y-auto overflow-x-visible px-2 py-2">
      {nodes.map((n) => (
        <RailItem key={n.label} node={n} onNavigate={onNavigate} />
      ))}
    </nav>
  )
}

function Brand() {
  return (
    <div className="flex h-14 items-center justify-center border-b">
      <div className="flex h-8 w-8 items-center justify-center rounded-full bg-primary text-primary-foreground">
        <ShieldCheck className="h-4 w-4" />
      </div>
    </div>
  )
}

// Account controls at the bottom of the rail: avatar + theme + sign-out, each
// with a hover tooltip (the rail is icon-sized).
function RailAccount() {
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
    <div className="flex flex-col items-center gap-1 border-t p-2">
      <Tooltip delayDuration={0}>
        <TooltipTrigger asChild>
          <div className="flex h-8 w-8 items-center justify-center rounded-full bg-primary text-xs font-semibold text-primary-foreground">
            {(email?.[0] ?? 'U').toUpperCase()}
          </div>
        </TooltipTrigger>
        <TooltipContent side="right">
          {email} · {topRole}
        </TooltipContent>
      </Tooltip>
      <Tooltip delayDuration={0}>
        <TooltipTrigger asChild>
          <Button variant="ghost" size="icon" className="h-8 w-8" onClick={toggleTheme}>
            {theme === 'dark' ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
          </Button>
        </TooltipTrigger>
        <TooltipContent side="right">{theme === 'dark' ? 'Light mode' : 'Dark mode'}</TooltipContent>
      </Tooltip>
      <Tooltip delayDuration={0}>
        <TooltipTrigger asChild>
          <Button variant="ghost" size="icon" className="h-8 w-8" onClick={signOut}>
            <LogOut className="h-4 w-4" />
          </Button>
        </TooltipTrigger>
        <TooltipContent side="right">Sign out</TooltipContent>
      </Tooltip>
    </div>
  )
}

// Mobile nav: a flat, expanded list inside the Sheet (no rail/flyout).
function MobileNav({ can, onNavigate }: { can: (perm: string) => boolean; onNavigate: () => void }) {
  const nodes = navTree
    .map((n): NavNode | null => {
      if (n.children) {
        const kids = n.children.filter((c) => !c.permission || can(c.permission))
        return kids.length ? { ...n, children: kids } : null
      }
      if (n.permission && !can(n.permission)) return null
      return n
    })
    .filter((n): n is NavNode => n != null)

  const leaf = (to: string, label: string, Icon: NavNode['icon'], indent?: boolean) => (
    <NavLink
      key={to}
      to={to}
      onClick={onNavigate}
      className={({ isActive }) =>
        cn(
          'flex items-center gap-3 rounded-md px-3 py-2 text-sm transition-colors',
          indent && 'pl-6',
          isActive
            ? 'bg-accent font-medium text-foreground'
            : 'font-normal text-muted-foreground hover:bg-accent hover:text-foreground',
        )
      }
    >
      <Icon className="h-4 w-4 shrink-0" />
      <span className="truncate">{label}</span>
    </NavLink>
  )

  return (
    <nav className="flex-1 space-y-3 overflow-y-auto p-3">
      {nodes.map((n) =>
        n.children ? (
          <div key={n.label}>
            <div className="px-2 pb-1 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
              {n.label}
            </div>
            <div className="space-y-0.5">
              {n.children.map((c) => leaf(c.to, c.label, c.icon, true))}
            </div>
          </div>
        ) : (
          n.to && leaf(n.to, n.label, n.icon)
        ),
      )}
    </nav>
  )
}

export function AppShell({ children }: { children: ReactNode }) {
  const { can } = useAuth()
  const [mobileOpen, setMobileOpen] = useState(false)

  return (
    <TooltipProvider delayDuration={0}>
      <div className="flex h-screen overflow-hidden bg-background text-foreground">
        {/* Desktop icon rail (fixed thin column). Sub-panels overlay content. */}
        <aside
          className={cn(
            'cg-sidebar sticky top-0 z-40 hidden h-screen shrink-0 flex-col border-r border-border md:flex',
            RAIL_W,
          )}
        >
          <Brand />
          <RailNav can={can} />
          <RailAccount />
        </aside>

        {/* Main column. */}
        <div className="flex min-w-0 flex-1 flex-col">
          <header className="flex h-14 items-center gap-3 border-b px-4 md:px-6">
            {/* Mobile menu */}
            <Sheet open={mobileOpen} onOpenChange={setMobileOpen}>
              <SheetTrigger asChild>
                <Button variant="ghost" size="icon" className="md:hidden" aria-label="Open menu">
                  <Menu className="h-4 w-4" />
                </Button>
              </SheetTrigger>
              <SheetContent side="left" className="cg-sidebar flex w-64 flex-col p-0">
                <div className="flex h-14 items-center gap-2 border-b px-5">
                  <div className="flex h-7 w-7 items-center justify-center rounded-full bg-primary text-primary-foreground">
                    <ShieldCheck className="h-4 w-4" />
                  </div>
                  <span className="text-base font-semibold tracking-tight">Bellu</span>
                </div>
                <MobileNav can={can} onNavigate={() => setMobileOpen(false)} />
              </SheetContent>
            </Sheet>

            {/* Page-provided header content renders here via <HeaderSlot>. */}
            <div id={HEADER_SLOT_ID} className="flex min-w-0 flex-1 items-center gap-3" />
          </header>

          <main className="flex-1 overflow-y-auto p-4 md:p-6">{children}</main>
        </div>
      </div>
    </TooltipProvider>
  )
}
