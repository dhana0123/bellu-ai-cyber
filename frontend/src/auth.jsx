import React, { createContext, useContext, useEffect, useState, useCallback } from 'react'
import { api, getToken, setToken } from './api.js'

const AuthContext = createContext(null)

// AuthProvider loads the current principal from a stored token, exposes login/
// logout, and a `can(permission)` helper for permission-gated UI.
export function AuthProvider({ children }) {
  const [principal, setPrincipal] = useState(null)
  const [loading, setLoading] = useState(true)

  const load = useCallback(async () => {
    if (!getToken()) {
      setLoading(false)
      return
    }
    try {
      const me = await api.me()
      setPrincipal(me)
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

  const login = async (email, password) => {
    const res = await api.login(email, password)
    setToken(res.token)
    setPrincipal({ user: res.user, memberships: res.memberships, permissions: res.permissions })
    return res
  }

  const logout = () => {
    setToken(null)
    setPrincipal(null)
  }

  const can = (perm) => !!principal?.permissions?.includes(perm)

  return (
    <AuthContext.Provider value={{ principal, loading, login, logout, can }}>
      {children}
    </AuthContext.Provider>
  )
}

export const useAuth = () => useContext(AuthContext)
