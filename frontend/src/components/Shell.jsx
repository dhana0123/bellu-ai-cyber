import React from 'react'
import { NavLink, useNavigate } from 'react-router-dom'
import { ShieldCheck, Moon, Sun, LogOut } from 'lucide-react'
import { navConfig } from '../config/nav.js'
import { useAuth } from '../auth.jsx'
import { useTheme } from '../theme.jsx'
import { cn } from '../lib/utils.js'
import { Button } from './ui/button.jsx'
import { Badge } from './ui/misc.jsx'

// Shell renders the config-driven sidebar + topbar around the routed page.
export default function Shell({ children }) {
  const { principal, logout, can } = useAuth()
  const { theme, setTheme } = useTheme()
  const navigate = useNavigate()

  const topRole = principal?.memberships?.[0]?.role_name || 'User'

  return (
    <div className="flex min-h-screen bg-background text-foreground">
      {/* Sidebar */}
      <aside className="hidden w-64 shrink-0 flex-col border-r bg-card/40 md:flex">
        <div className="flex h-14 items-center gap-2 border-b px-5">
          <div className="flex h-7 w-7 items-center justify-center rounded-md bg-foreground text-background">
            <ShieldCheck className="h-4 w-4" />
          </div>
          <span className="font-semibold tracking-tight">Bellu</span>
        </div>

        <nav className="flex-1 space-y-6 overflow-y-auto p-3">
          {navConfig.map((group) => {
            const items = group.items.filter((it) => !it.permission || can(it.permission))
            if (items.length === 0) return null
            return (
              <div key={group.section}>
                <p className="px-2 pb-1 text-xs font-medium uppercase tracking-wider text-muted-foreground">
                  {group.section}
                </p>
                <div className="space-y-0.5">
                  {items.map((it) => (
                    <NavLink
                      key={it.to}
                      to={it.to}
                      end={it.end}
                      className={({ isActive }) =>
                        cn(
                          'flex items-center gap-3 rounded-md px-2.5 py-2 text-sm font-medium transition-colors',
                          isActive
                            ? 'bg-accent text-foreground'
                            : 'text-muted-foreground hover:bg-accent/60 hover:text-foreground',
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

        <div className="border-t p-3 text-xs text-muted-foreground">
          Evidence-backed AI security graph
        </div>
      </aside>

      {/* Main */}
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-14 items-center justify-between border-b px-4 md:px-6">
          <div className="flex items-center gap-2">
            <span className="text-sm text-muted-foreground">Signed in as</span>
            <span className="text-sm font-medium">{principal?.user?.email}</span>
            <Badge variant="ai">{topRole}</Badge>
          </div>
          <div className="flex items-center gap-1">
            <Button variant="ghost" size="icon" onClick={() => setTheme(theme === 'dark' ? 'light' : 'dark')} title="Toggle theme">
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
