// Client for the Bellu control-panel API. Attaches the stored JWT as a Bearer
// token. The base URL is empty so requests hit the same origin (dev proxies
// /api to the backend; in prod nginx proxies it).
const BASE = import.meta.env.VITE_API_BASE || ''

const TOKEN_KEY = 'bellu-token'

export function getToken() {
  return localStorage.getItem(TOKEN_KEY)
}
export function setToken(t) {
  if (t) localStorage.setItem(TOKEN_KEY, t)
  else localStorage.removeItem(TOKEN_KEY)
}

async function request(method, path, body) {
  const headers = {}
  const token = getToken()
  if (token) headers['Authorization'] = `Bearer ${token}`
  if (body !== undefined) headers['Content-Type'] = 'application/json'

  const res = await fetch(`${BASE}${path}`, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })

  if (res.status === 401) {
    // Token invalid/expired — clear it so the app redirects to login.
    setToken(null)
    throw new Error('unauthorized')
  }
  if (!res.ok) {
    let detail = res.statusText
    try {
      const b = await res.json()
      if (b.error) detail = b.error
    } catch {
      /* ignore */
    }
    throw new Error(detail)
  }
  if (res.status === 204) return null
  return res.json()
}

const get = (p) => request('GET', p)
const post = (p, b) => request('POST', p, b)
const patch = (p, b) => request('PATCH', p, b)

export const api = {
  // auth
  login: (email, password) => post('/api/auth/login', { email, password }),
  me: () => get('/api/auth/me'),

  // graph / evidence
  health: () => get('/api/health'),
  stats: () => get('/api/stats'),
  entities: (kind, limit = 300) =>
    get(`/api/entities?limit=${limit}${kind ? `&kind=${encodeURIComponent(kind)}` : ''}`),
  runs: (limit = 50) => get(`/api/runs?limit=${limit}`),
  conflicts: (limit = 100) => get(`/api/conflicts?limit=${limit}`),
  attackPaths: () => get('/api/graph/attack-paths'),
  aiCalls: () => get('/api/graph/ai-calls'),
  findEdges: (relation, like, limit = 100) =>
    get(
      `/api/edges/find?limit=${limit}` +
        `${relation ? `&relation=${encodeURIComponent(relation)}` : ''}` +
        `${like ? `&like=${encodeURIComponent(like)}` : ''}`,
    ),
  explainEdge: (id) => get(`/api/edges/explain?id=${id}`),

  // findings / intelligence
  findings: (filters = {}) => {
    const q = new URLSearchParams()
    if (filters.severity) q.set('severity', filters.severity)
    if (filters.status) q.set('status', filters.status)
    if (filters.asset_kind) q.set('asset_kind', filters.asset_kind)
    q.set('limit', filters.limit ?? 200)
    return get(`/api/findings?${q.toString()}`)
  },
  finding: (id) => get(`/api/findings/${id}`),
  intelVulns: (filters = {}) => {
    const q = new URLSearchParams()
    if (filters.severity) q.set('severity', filters.severity)
    if (filters.kev !== undefined && filters.kev !== '') q.set('kev', filters.kev)
    if (filters.purl) q.set('purl', filters.purl)
    const qs = q.toString()
    return get(`/api/intel/vulnerabilities${qs ? `?${qs}` : ''}`)
  },
  intelVuln: (id) => get(`/api/intel/vulnerabilities/${encodeURIComponent(id)}`),

  // admin
  users: () => get('/api/users'),
  createUser: (payload) => post('/api/users', payload),
  setUserActive: (id, active) => patch(`/api/users/${id}/active`, { active }),
  roles: () => get('/api/roles'),
  createRole: (payload) => post('/api/roles', payload),
  permissions: () => get('/api/permissions'),
  projects: () => get('/api/projects'),
  createProject: (key, name) => post('/api/projects', { key, name }),
}
