import { useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { ShieldCheck } from 'lucide-react'
import { useAuth } from '@/auth'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

// Split-screen login: left = form, right = brand panel. Behavior preserved from
// the pre-migration page.
export default function Login() {
  const { login } = useAuth()
  const navigate = useNavigate()
  const [email, setEmail] = useState('admin@bellu.local')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    setBusy(true)
    try {
      await login(email, password)
      navigate('/', { replace: true })
    } catch (err) {
      const msg = (err as Error).message
      setError(msg === 'invalid email or password' ? 'Invalid email or password' : msg)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="grid min-h-screen lg:grid-cols-2">
      {/* Left: form */}
      <div className="flex flex-col gap-4 p-6 md:p-10">
        <div className="flex items-center gap-2 font-medium">
          {/* Brand mark: Bellu Violet → Logo Teal gradient. */}
          <div
            className="flex h-7 w-7 items-center justify-center rounded-md text-white shadow-sm"
            style={{ backgroundImage: 'linear-gradient(135deg, #8B5CF6 0%, #2BC8B7 100%)' }}
          >
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
                <Input
                  id="email"
                  type="email"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  autoComplete="username"
                  required
                  placeholder="you@company.com"
                />
              </div>
              <div className="space-y-1.5">
                <div className="flex items-center justify-between">
                  <Label htmlFor="password">Password</Label>
                  <span className="text-xs text-muted-foreground">Forgot?</span>
                </div>
                <Input
                  id="password"
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  autoComplete="current-password"
                  required
                />
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

      {/* Right: brand panel — White → Bellu Violet brand gradient. */}
      <div
        className="relative hidden overflow-hidden border-l lg:block"
        style={{ backgroundImage: 'linear-gradient(160deg, #FFFFFF 0%, #DDD6FE 45%, #8B5CF6 100%)' }}
      >
        <div
          className="absolute inset-0 opacity-[0.06]"
          style={{
            backgroundImage:
              'linear-gradient(#2E1065 1px, transparent 1px), linear-gradient(90deg, #2E1065 1px, transparent 1px)',
            backgroundSize: '32px 32px',
          }}
        />
        <div className="relative flex h-full flex-col justify-end p-12">
          <blockquote className="space-y-3">
            <p className="text-xl font-medium leading-relaxed text-[#2E1065]">
              See every AI agent, model and MCP server — and the exact path from the internet to
              your sensitive data.
            </p>
            <footer className="text-sm font-medium text-[#2E1065]/70">Bellu — AI Security Graph</footer>
          </blockquote>
        </div>
      </div>
    </div>
  )
}
