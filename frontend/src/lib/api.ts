// Typed client for the REAL Bellu control-panel API. Mirrors the pre-migration
// api.js surface (same endpoints, same token/401 behavior) but typed against
// types/foundation.ts. Base URL empty => same-origin (dev proxy / nginx).
import { API_BASE } from './env'
import { getToken, setToken } from './token'
import type {
  AiCallRow,
  AttackPathRow,
  CanonicalVuln,
  CapabilityCard,
  Connection,
  ConnectorStatusView,
  CreateConnectorInput,
  CreateUserInput,
  DiscoverySession,
  EdgeDetail,
  EntityRow,
  Finding,
  FindingDetailResponse,
  HealthResponse,
  LoginResponse,
  MeResponse,
  PermissionRow,
  ProjectRow,
  RoleRow,
  Setup,
  Stats,
  UserRow,
  VerifyResult,
} from '@/types/foundation'

type Method = 'GET' | 'POST' | 'PATCH' | 'DELETE'

async function request<T>(method: Method, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {}
  const token = getToken()
  if (token) headers['Authorization'] = `Bearer ${token}`
  if (body !== undefined) headers['Content-Type'] = 'application/json'

  const res = await fetch(`${API_BASE}${path}`, {
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
  if (res.status === 204) return null as T
  return (await res.json()) as T
}

const get = <T>(p: string) => request<T>('GET', p)
const post = <T>(p: string, b?: unknown) => request<T>('POST', p, b)
const patch = <T>(p: string, b?: unknown) => request<T>('PATCH', p, b)
const del = <T>(p: string) => request<T>('DELETE', p)

export interface FindingsFilter {
  severity?: string
  status?: string
  asset_kind?: string
  limit?: number
}

export interface IntelFilter {
  severity?: string
  kev?: boolean | ''
  purl?: string
}

// The typed real API. Grouped by domain to match the pre-migration client.
export const realApi = {
  // auth
  login: (email: string, password: string) =>
    post<LoginResponse>('/api/auth/login', { email, password }),
  me: () => get<MeResponse>('/api/auth/me'),

  // graph / evidence
  health: () => get<HealthResponse>('/api/health'),
  stats: () => get<Stats>('/api/stats'),
  entities: (kind?: string, limit = 300, projectId?: number) =>
    get<{ entities: EntityRow[] }>(
      `/api/entities?limit=${limit}` +
        (kind ? `&kind=${encodeURIComponent(kind)}` : '') +
        (projectId != null ? `&project_id=${projectId}` : ''),
    ),
  runs: (limit = 50) => get<{ runs: unknown[] }>(`/api/runs?limit=${limit}`),
  conflicts: (limit = 100) => get<{ conflicts: unknown[] }>(`/api/conflicts?limit=${limit}`),
  attackPaths: () => get<{ paths: AttackPathRow[] }>('/api/graph/attack-paths'),
  aiCalls: () => get<{ calls: AiCallRow[] }>('/api/graph/ai-calls'),
  findEdges: (relation?: string, like?: string, limit = 100) =>
    get<{ edges: EdgeDetail[] }>(
      `/api/edges/find?limit=${limit}` +
        `${relation ? `&relation=${encodeURIComponent(relation)}` : ''}` +
        `${like ? `&like=${encodeURIComponent(like)}` : ''}`,
    ),
  explainEdge: (id: number | string) => get<EdgeDetail>(`/api/edges/explain?id=${id}`),

  // findings / intelligence
  findings: (filters: FindingsFilter = {}) => {
    const q = new URLSearchParams()
    if (filters.severity) q.set('severity', filters.severity)
    if (filters.status) q.set('status', filters.status)
    if (filters.asset_kind) q.set('asset_kind', filters.asset_kind)
    q.set('limit', String(filters.limit ?? 200))
    return get<{ findings: Finding[] }>(`/api/findings?${q.toString()}`)
  },
  finding: (id: number | string) => get<FindingDetailResponse>(`/api/findings/${id}`),
  intelVulns: (filters: IntelFilter = {}) => {
    const q = new URLSearchParams()
    if (filters.severity) q.set('severity', filters.severity)
    if (filters.kev !== undefined && filters.kev !== '') q.set('kev', String(filters.kev))
    if (filters.purl) q.set('purl', filters.purl)
    const qs = q.toString()
    return get<{ vulnerabilities: CanonicalVuln[] }>(
      `/api/intel/vulnerabilities${qs ? `?${qs}` : ''}`,
    )
  },
  intelVuln: (id: string) =>
    get<{ vulnerability: CanonicalVuln }>(`/api/intel/vulnerabilities/${encodeURIComponent(id)}`),

  // admin
  users: () => get<{ users: UserRow[] }>('/api/users'),
  createUser: (payload: CreateUserInput) => post<{ id: number }>('/api/users', payload),
  setUserActive: (id: number, active: boolean) =>
    patch<{ ok: true }>(`/api/users/${id}/active`, { active }),
  roles: () => get<{ roles: RoleRow[] }>('/api/roles'),
  createRole: (payload: Partial<RoleRow>) => post<{ id: number }>('/api/roles', payload),
  permissions: () => get<{ permissions: PermissionRow[] }>('/api/permissions'),
  projects: () => get<{ projects: ProjectRow[] }>('/api/projects'),
  createProject: (key: string, name: string) =>
    post<{ id: number }>('/api/projects', { key, name }),

  // cloud connectors
  connectorCapabilities: () =>
    get<{ capabilities: CapabilityCard[] }>('/api/connectors/capabilities'),
  connectors: () => get<{ connections: Connection[] }>('/api/connectors'),
  connector: (id: number | string) => get<{ connection: Connection }>(`/api/connectors/${id}`),
  connectorStatus: (id: number | string) =>
    get<{ status: ConnectorStatusView }>(`/api/connectors/${id}/status`),
  createConnector: (payload: CreateConnectorInput) =>
    post<{ connection: Connection }>('/api/connectors', payload),
  connectorSetup: (id: number | string) => post<{ setup: Setup }>(`/api/connectors/${id}/setup`),
  verifyConnector: (id: number | string, roleArn: string) =>
    post<{ verification: VerifyResult }>(`/api/connectors/${id}/verify`, { role_arn: roleArn }),
  discoverConnector: (id: number | string) =>
    post<{ session: DiscoverySession }>(`/api/connectors/${id}/discover`),
  // Absolute URL for the SSE progress stream. EventSource cannot set an
  // Authorization header, so the JWT travels in the access_token query param
  // (the API's Authenticate middleware accepts it there for this endpoint).
  discoverStreamUrl: (id: number | string) => {
    const token = getToken()
    const q = token ? `?access_token=${encodeURIComponent(token)}` : ''
    return `${API_BASE}/api/connectors/${id}/discover/stream${q}`
  },
  cancelConnector: (id: number | string) => post(`/api/connectors/${id}/cancel`),
  expandConnector: (id: number | string, memberAccountIds: string[]) =>
    post<{ connections: Connection[] }>(`/api/connectors/${id}/expand`, {
      member_account_ids: memberAccountIds,
    }),
  deleteConnector: (id: number | string) => del(`/api/connectors/${id}`),
}

export type RealApi = typeof realApi
