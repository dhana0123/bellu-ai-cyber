// JWT storage. Single source of truth for the token key, shared by the typed
// API client and the auth provider. Matches the pre-migration key so sessions
// survive the migration.
const TOKEN_KEY = 'bellu-token'

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY)
}

export function setToken(t: string | null): void {
  if (t) localStorage.setItem(TOKEN_KEY, t)
  else localStorage.removeItem(TOKEN_KEY)
}
