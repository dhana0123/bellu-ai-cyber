import { useState, type ReactNode } from 'react'
import { NavLink, useNavigate } from 'react-router-dom'
import { ShieldCheck, Moon, Sun, LogOut, Menu } from 'lucide-react'
import { navConfig, type NavGroup } from '@/config/nav'
import { useAuth } from '@/auth'
import { useTheme } from '@/theme'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Sheet, SheetContent, SheetTrigger } from '@/components/ui/sheet'

// AppShell renders the config-driven sidebar + topbar around the routed page.
// Successor to the pre-migration Shell.jsx; preserves the ChatGPT-style
// sidebar (.cg-sidebar), permission-filtered nav, and the theme toggle / user
// badge / sign-out controls. Adds a mobile sidebar via Sheet and a "Preview"
// badge on the add-ons section.

function SidebarNav({
  can,
  onNavigate,
}: {
  can: (perm: string) => boolean
  onNavigate?: () => void
}) {
  return (
    <nav className="flex-1 space-y-6 overflow-y-auto p-3">
      {navConfig.map((group: NavGroup) => {
        const items = group.items.filter((it) => !it.permission || can(it.permission))
        if (items.length === 0) return null
        return (
          <div key={group.section}>
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
            <div className="space-y-0.5">
              {items.map((it) => (
                <NavLink
                  key={it.to}
                  to={it.to}
                  end={it.end}
                  onClick={onNavigate}
                  className={({ isActive }) =>
                    cn(
                      'flex items-center gap-3 rounded-lg px-3 py-2 text-sm transition-colors',
                      isActive
                        ? 'bg-accent font-medium text-foreground'
                        : 'font-normal text-muted-foreground hover:bg-accent hover:text-foreground',
                    )
                  }
                >
                  <it.icon className="h-4 w-4" />
                  {it.label}
                </NavLink>
              ))}
            </div>
          </div>
        )
      })}
    </nav>
  )
}

function Brand() {
  return (
    <div className="flex h-14 items-center gap-2 border-b px-5">
      <div className="flex h-7 w-7 items-center justify-center rounded-full bg-primary text-primary-foreground">
        <ShieldCheck className="h-4 w-4" />
      </div>
      <span className="text-base font-semibold tracking-tight">Bellu</span>
    </div>
  )
}

export function AppShell({ children }: { children: ReactNode }) {
  const { principal, logout, can } = useAuth()
  const { theme, setTheme } = useTheme()
  const navigate = useNavigate()
  const [mobileOpen, setMobileOpen] = useState(false)

  const topRole = principal?.memberships?.[0]?.role_name || 'User'

  return (
    <div className="flex h-screen overflow-hidden bg-background text-foreground">
      {/* Desktop sidebar */}
      <aside className="cg-sidebar sticky top-0 hidden h-screen w-64 shrink-0 flex-col border-r border-border md:flex">
        <Brand />
        <SidebarNav can={can} />
        <div className="border-t p-3 text-xs text-muted-foreground">
          Evidence-backed AI security graph
        </div>
      </aside>

      {/* Main column */}
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-14 items-center justify-between border-b px-4 md:px-6">
          <div className="flex items-center gap-2">
            {/* Mobile menu */}
            <Sheet open={mobileOpen} onOpenChange={setMobileOpen}>
              <SheetTrigger asChild>
                <Button variant="ghost" size="icon" className="md:hidden" aria-label="Open menu">
                  <Menu className="h-4 w-4" />
                </Button>
              </SheetTrigger>
              <SheetContent side="left" className="cg-sidebar w-64 p-0">
                <Brand />
                <SidebarNav can={can} onNavigate={() => setMobileOpen(false)} />
              </SheetContent>
            </Sheet>

            <span className="hidden text-sm text-muted-foreground sm:inline">Signed in as</span>
            <span className="text-sm font-medium">{principal?.user?.email}</span>
            <Badge variant="ai">{topRole}</Badge>
          </div>
          <div className="flex items-center gap-1">
            <Button
              variant="ghost"
              size="icon"
              onClick={() => setTheme(theme === 'dark' ? 'light' : 'dark')}
              title="Toggle theme"
            >
              {theme === 'dark' ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
            </Button>
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                logout()
                navigate('/login', { replace: true })
              }}
            >
              <LogOut className="h-4 w-4" /> Sign out
            </Button>
          </div>
        </header>

        <main className="flex-1 overflow-y-auto p-4 md:p-6">{children}</main>
      </div>
    </div>
  )
}
