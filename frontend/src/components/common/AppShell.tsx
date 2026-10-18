import { useEffect, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { NavLink, useNavigate } from 'react-router-dom'
import { ShieldCheck, Moon, Sun, LogOut } from 'lucide-react'
import { navTree, type NavNode } from '@/config/nav'
import { useAuth } from '@/auth'
import { useTheme } from '@/theme'
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

function AppSidebar({ can }: { can: (perm: string) => boolean }) {
  const nodes = visibleNodes(can)
  return (
    <Sidebar collapsible="icon" className="cg-sidebar">
      <SidebarHeader>
        <div className="flex h-8 items-center gap-2 px-1">
          <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-primary text-primary-foreground">
            <ShieldCheck className="h-4 w-4" />
          </div>
          <span className="truncate text-base font-semibold tracking-tight group-data-[collapsible=icon]:hidden">
            Bellu
          </span>
        </div>
      </SidebarHeader>

      <SidebarContent>
        {nodes.map((n) =>
          n.children ? (
            <SidebarGroup key={n.label}>
              <SidebarGroupLabel>
                {n.label}
                {n.preview ? (
                  <Badge variant="warning" className="ml-1.5 px-1.5 py-0 text-[10px]">
                    Preview
                  </Badge>
                ) : null}
              </SidebarGroupLabel>
              <SidebarMenu>
                {n.children.map((c) => (
                  <NavItem key={c.to} to={c.to} label={c.label} Icon={c.icon} end={c.end} />
                ))}
              </SidebarMenu>
            </SidebarGroup>
          ) : (
            n.to && (
              <SidebarGroup key={n.to}>
                <SidebarMenu>
                  <NavItem to={n.to} label={n.label} Icon={n.icon} end={n.end} />
                </SidebarMenu>
              </SidebarGroup>
            )
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

  return (
    <SidebarProvider>
      <AppSidebar can={can} />
      <SidebarInset>
        <header className="flex h-14 items-center gap-3 border-b px-4 md:px-6">
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
