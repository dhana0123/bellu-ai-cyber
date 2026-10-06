// Thin client for the Bellu control-panel API. The base URL is empty so
// requests go to the same origin (dev server proxies /api to the backend;
// in production the API is served behind the same host).
const BASE = import.meta.env.VITE_API_BASE || ''

async function get(path) {
  const res = await fetch(`${BASE}${path}`)
  if (!res.ok) {
    let detail = res.statusText
    try {
      const body = await res.json()
      if (body.error) detail = body.error
    } catch {
      /* ignore */
    }
    throw new Error(`${res.status}: ${detail}`)
  }
  return res.json()
}

export const api = {
  health: () => get('/api/health'),
  stats: () => get('/api/stats'),
  entities: (kind, limit = 200) =>
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
}
