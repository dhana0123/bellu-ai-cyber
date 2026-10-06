import React, { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { ShieldCheck } from 'lucide-react'
import { useAuth } from '../auth.jsx'
import { Button } from '../components/ui/button.jsx'
import { Input, Label } from '../components/ui/input.jsx'

// login-04 style: split screen. Left = form, right = brand/visual panel.
export default function Login() {
  const { login } = useAuth()
  const navigate = useNavigate()
  const [email, setEmail] = useState('admin@bellu.local')
  const [password, setPassword] = useState('')
  const [error, setError] = useState(null)
  const [busy, setBusy] = useState(false)

  async function onSubmit(e) {
    e.preventDefault()
    setError(null)
    setBusy(true)
    try {
      await login(email, password)
      navigate('/', { replace: true })
    } catch (err) {
      setError(err.message === 'invalid email or password' ? 'Invalid email or password' : err.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="grid min-h-screen lg:grid-cols-2">
      {/* Left: form */}
      <div className="flex flex-col gap-4 p-6 md:p-10">
        <div className="flex items-center gap-2 font-medium">
          <div className="flex h-7 w-7 items-center justify-center rounded-md bg-foreground text-background">
            <ShieldCheck className="h-4 w-4" />
          </div>
          Bellu
        </div>

        <div className="flex flex-1 items-center justify-center">
          <form onSubmit={onSubmit} className="w-full max-w-sm space-y-6">
            <div className="space-y-1.5 text-center">
              <h1 className="text-2xl font-semibold tracking-tight">Sign in to your account</h1>
              <p className="text-sm text-muted-foreground">
                Enter your email below to access the control panel
              </p>
            </div>

            <div className="space-y-4">
              <div className="space-y-1.5">
                <Label htmlFor="email">Email</Label>
                <Input id="email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="username" required placeholder="you@company.com" />
              </div>
              <div className="space-y-1.5">
                <div className="flex items-center justify-between">
                  <Label htmlFor="password">Password</Label>
                  <span className="text-xs text-muted-foreground">Forgot?</span>
                </div>
                <Input id="password" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" required />
              </div>

              {error && <p className="text-sm text-destructive">{error}</p>}

              <Button type="submit" className="w-full" disabled={busy}>
                {busy ? 'Signing in…' : 'Sign in'}
              </Button>
            </div>

            <p className="text-center text-xs text-muted-foreground">
              Protected area. Access is role-based and audited.
            </p>
          </form>
        </div>
      </div>

      {/* Right: brand panel */}
      <div className="relative hidden overflow-hidden border-l bg-muted/30 lg:block">
        <div className="absolute inset-0 bg-[radial-gradient(circle_at_30%_20%,hsl(var(--foreground)/0.06),transparent_55%)]" />
        <div
          className="absolute inset-0 opacity-[0.04]"
          style={{
            backgroundImage:
              'linear-gradient(hsl(var(--foreground)) 1px, transparent 1px), linear-gradient(90deg, hsl(var(--foreground)) 1px, transparent 1px)',
            backgroundSize: '32px 32px',
          }}
        />
        <div className="relative flex h-full flex-col justify-end p-12">
          <blockquote className="space-y-3">
            <p className="text-xl font-medium leading-relaxed">
              See every AI agent, model and MCP server — and the exact path from the internet to
              your sensitive data.
            </p>
            <footer className="text-sm text-muted-foreground">
              Bellu — AI Security Graph
            </footer>
          </blockquote>
        </div>
      </div>
    </div>
  )
}
