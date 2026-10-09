import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from 'react'
import { realApi } from '@/lib/api'
import { getToken, setToken } from '@/lib/token'
import { IS_DEV } from '@/lib/env'
import type { LoginResponse, MeResponse, Principal } from '@/types/foundation'
import { ADDON_PERMISSIONS } from '@/types/addons'

interface AuthContextValue {
  principal: Principal | null
  loading: boolean
  login: (email: string, password: string) => Promise<LoginResponse>
  logout: () => void
  can: (perm: string) => boolean
}

const AuthContext = createContext<AuthContextValue | null>(null)

// Normalize both the login response and the /me response into one Principal
// shape. The login response additionally carries token/expires_at (stored
// separately), but the user/memberships/permissions fields align.
function toPrincipal(src: MeResponse | LoginResponse): Principal {
  const permissions = src.permissions ?? []
  return {
    user: src.user,
    memberships: src.memberships ?? [],
    // DEV ONLY: the backend has no add-on permissions yet, so in dev we grant
    // the Preview permissions to any authenticated user so the mock add-on
    // modules are reachable. In production these are omitted — add-ons remain
    // gated behind the real permissions once a backend ships them. This never
    // fabricates data; it only reveals the demo-labeled Preview UI.
    permissions: IS_DEV ? [...permissions, ...ADDON_PERMISSIONS] : permissions,
  }
}

// AuthProvider loads the current principal from a stored token, exposes login/
// logout, and a `can(permission)` helper for permission-gated UI. Token/401
// behavior preserved from the pre-migration version.
export function AuthProvider({ children }: { children: ReactNode }) {
  const [principal, setPrincipal] = useState<Principal | null>(null)
  const [loading, setLoading] = useState(true)

  const load = useCallback(async () => {
    if (!getToken()) {
      setLoading(false)
      return
    }
    try {
      const me = await realApi.me()
      setPrincipal(toPrincipal(me))
    } catch {
      setToken(null)
      setPrincipal(null)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    load()
  }, [load])

  const login = async (email: string, password: string) => {
    const res = await realApi.login(email, password)
    setToken(res.token)
    setPrincipal(toPrincipal(res))
    return res
  }

  const logout = () => {
    setToken(null)
    setPrincipal(null)
  }

  const can = (perm: string) => !!principal?.permissions?.includes(perm)

  return (
    <AuthContext.Provider value={{ principal, loading, login, logout, can }}>
      {children}
    </AuthContext.Provider>
  )
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within an AuthProvider')
  return ctx
}
