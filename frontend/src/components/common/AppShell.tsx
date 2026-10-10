import { useEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { NavLink, useNavigate } from 'react-router-dom'
import {
  ShieldCheck,
  Moon,
  Sun,
  LogOut,
  Menu,
  PanelLeftClose,
  PanelLeftOpen,
} from 'lucide-react'
import { navConfig, type NavGroup } from '@/config/nav'
import { useAuth } from '@/auth'
import { useTheme } from '@/theme'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Sheet, SheetContent, SheetTrigger } from '@/components/ui/sheet'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'

// AppShell renders the config-driven sidebar + topbar around the routed page.
// The sidebar collapses to a compact icon rail (300ms width transition); when
// collapsed, nav items show shadcn-style hover tooltips. The main column flexes,
// so the canvas viewport expands smoothly into the freed space. The top header
// exposes a portal slot ("app-header-slot") that pages fill with their own
// title/controls (e.g. the Dashboard title + Grid/Map tabs).

const HEADER_SLOT_ID = 'app-header-slot'
const COLLAPSE_KEY = 'bellu.sidebar.collapsed'

// HeaderSlot: pages render into the shell's top header via this portal.
export function HeaderSlot({ children }: { children: ReactNode }) {
  const [el, setEl] = useState<HTMLElement | null>(null)
  useEffect(() => {
    setEl(document.getElementById(HEADER_SLOT_ID))
  }, [])
  if (!el) return null
  return createPortal(children, el)
}

function SidebarNav({
  can,
  collapsed,
  onNavigate,
}: {
  can: (perm: string) => boolean
  collapsed: boolean
  onNavigate?: () => void
}) {
  return (
    <nav
      className={cn(
        'flex-1 overflow-y-auto overflow-x-hidden py-2',
        // shadcn group rhythm: gap-2 between groups in the rail; hidden scrollbar.
        collapsed ? 'sidebar-rail-scroll space-y-2 px-2' : 'space-y-6 px-3',
      )}
    >
      {navConfig.map((group: NavGroup) => {
        const items = group.items.filter((it) => !it.permission || can(it.permission))
        if (items.length === 0) return null
        return (
          <div key={group.section}>
            {/* Section header: hidden when collapsed (a thin divider stands in). */}
            {collapsed ? (
              <div className="mx-auto mb-2 h-px w-6 bg-border" />
            ) : (
              <div className="flex items-center gap-2 px-2 pb-1">
                <p className="text-xs font-medium uppercase tracking-wider text-muted-foreground">
                  {group.section}
                </p>
                {group.preview ? (
                  <Badge variant="warning" className="px-1.5 py-0 text-[10px]">
                    Preview
                  </Badge>
                ) : null}
              </div>
            )}
            {/* shadcn SidebarMenu: flex flex-col gap-1 */}
            <div className={cn(collapsed ? 'flex flex-col items-center gap-1' : 'space-y-0.5')}>
              {items.map((it) => {
                const link = (
                  <NavLink
                    to={it.to}
                    end={it.end}
                    onClick={onNavigate}
                    className={({ isActive }) =>
                      cn(
                        'flex items-center overflow-hidden rounded-md text-sm transition-colors',
                        // Collapsed: shadcn icon-mode button — a locked 32px square
                        // (size-8 + p-2 + shrink-0 + aspect-square) so the hover /
                        // active background is a clean square, never squashed.
                        collapsed
                          ? 'aspect-square h-8 w-8 shrink-0 justify-center p-2'
                          : 'w-full gap-3 px-3 py-2',
                        isActive
                          ? 'bg-accent font-medium text-foreground'
                          : 'font-normal text-muted-foreground hover:bg-accent hover:text-foreground',
                      )
                    }
                  >
                    {/* shadcn menu icon = size-4 (16px), never shrinks */}
                    <it.icon className="h-4 w-4 shrink-0" />
                    {!collapsed && <span className="truncate">{it.label}</span>}
                  </NavLink>
                )

                // When collapsed, each icon gets a shadcn hover tooltip.
                return collapsed ? (
                  <Tooltip key={it.to} delayDuration={0}>
                    <TooltipTrigger asChild>{link}</TooltipTrigger>
                    <TooltipContent side="right">{it.label}</TooltipContent>
                  </Tooltip>
                ) : (
                  <div key={it.to}>{link}</div>
                )
              })}
            </div>
          </div>
        )
      })}
    </nav>
  )
}

function Brand({
  collapsed,
  onToggle,
}: {
  collapsed: boolean
  onToggle?: () => void
}) {
  return (
    <div className={cn('flex h-14 items-center border-b', collapsed ? 'justify-center px-2' : 'gap-2 px-5')}>
      <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-primary text-primary-foreground">
        <ShieldCheck className="h-4 w-4" />
      </div>
      {!collapsed && <span className="flex-1 text-base font-semibold tracking-tight">Bellu</span>}
      {onToggle ? (
        <Tooltip delayDuration={0}>
          <TooltipTrigger asChild>
            <Button
              variant="ghost"
              size="icon"
              className={cn('h-8 w-8', collapsed && 'hidden')}
              onClick={onToggle}
              aria-label="Collapse sidebar"
            >
              <PanelLeftClose className="h-4 w-4" />
            </Button>
          </TooltipTrigger>
          <TooltipContent side="right">Collapse sidebar</TooltipContent>
        </Tooltip>
      ) : null}
    </div>
  )
}

// SidebarAccount: account/user controls at the bottom-left of the sidebar.
// Collapses to a single avatar/sign-out icon stack with tooltips.
function SidebarAccount({ collapsed }: { collapsed: boolean }) {
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

  if (collapsed) {
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

  return (
    <div className="space-y-2 border-t p-3">
      <div className="flex items-center gap-2">
        <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-primary text-xs font-semibold text-primary-foreground">
          {(email?.[0] ?? 'U').toUpperCase()}
        </div>
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-medium">{email}</div>
          <Badge variant="ai" className="mt-0.5 px-1.5 py-0 text-[10px]">
            {topRole}
          </Badge>
        </div>
      </div>
      <div className="flex items-center gap-1">
        <Button variant="ghost" size="sm" className="flex-1 justify-start" onClick={toggleTheme}>
          {theme === 'dark' ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
          {theme === 'dark' ? 'Light' : 'Dark'}
        </Button>
        <Button variant="ghost" size="sm" className="flex-1 justify-start" onClick={signOut}>
          <LogOut className="h-4 w-4" /> Sign out
        </Button>
      </div>
    </div>
  )
}

export function AppShell({ children }: { children: ReactNode }) {
  const { can } = useAuth()
  const [mobileOpen, setMobileOpen] = useState(false)
  const [collapsed, setCollapsed] = useState<boolean>(() => {
    try {
      return localStorage.getItem(COLLAPSE_KEY) === '1'
    } catch {
      return false
    }
  })
  const mainRef = useRef<HTMLElement>(null)

  const toggleCollapsed = () =>
    setCollapsed((c) => {
      const next = !c
      try {
        localStorage.setItem(COLLAPSE_KEY, next ? '1' : '0')
      } catch {
        /* ignore */
      }
      return next
    })

  return (
    <TooltipProvider delayDuration={0}>
      <div className="flex h-screen overflow-hidden bg-background text-foreground">
        {/* Desktop sidebar — animates width on collapse/expand. */}
        <aside
          className={cn(
            'cg-sidebar sticky top-0 hidden h-screen shrink-0 flex-col overflow-hidden border-r border-border transition-[width] duration-300 ease-in-out md:flex',
            collapsed ? 'w-16' : 'w-64',
          )}
        >
          <Brand collapsed={collapsed} onToggle={toggleCollapsed} />
          <SidebarNav can={can} collapsed={collapsed} />
          <SidebarAccount collapsed={collapsed} />
        </aside>

        {/* Main column — flexes to fill the space freed by the sidebar. */}
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
                <Brand collapsed={false} />
                <SidebarNav can={can} collapsed={false} onNavigate={() => setMobileOpen(false)} />
                <SidebarAccount collapsed={false} />
              </SheetContent>
            </Sheet>

            {/* Expand button — only visible when collapsed (desktop). */}
            {collapsed && (
              <Tooltip delayDuration={0}>
                <TooltipTrigger asChild>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="hidden md:inline-flex"
                    onClick={toggleCollapsed}
                    aria-label="Expand sidebar"
                  >
                    <PanelLeftOpen className="h-4 w-4" />
                  </Button>
                </TooltipTrigger>
                <TooltipContent side="right">Expand sidebar</TooltipContent>
              </Tooltip>
            )}

            {/* Page-provided header content renders here via <HeaderSlot>. */}
            <div id={HEADER_SLOT_ID} className="flex min-w-0 flex-1 items-center gap-3" />
          </header>

          <main ref={mainRef} className="flex-1 overflow-y-auto p-4 md:p-6">
            {children}
          </main>
        </div>
      </div>
    </TooltipProvider>
  )
}
