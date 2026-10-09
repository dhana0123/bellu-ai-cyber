// TypeScript shapes for the REAL Bellu backend API. Casing is deliberate and
// matches Go serialization exactly (see .kiro/specs/bellu-ui-ux/01-...md §2.3):
//   - PascalCase for findings/intel/evidence/edges (Go structs with no json tags)
//   - snake_case for stats/entities/runs/connectors/auth
// Do not "normalize" these names — the client must read what the server sends.

// ---------------------------------------------------------------------------
// Enums / unions (from internal/model, internal/auth, global-intel/canonical)
// ---------------------------------------------------------------------------

export const ENTITY_KINDS = [
  'CloudAccount',
  'Identity',
  'DataStore',
  'Compute',
  'Cluster',
  'Workload',
  'ContainerImage',
  'Repository',
  'Package',
  'Vulnerability',
  'AIAgent',
  'AIModel',
  'Prompt',
  'MCPServer',
  'AIService',
  'NetworkEndpoint',
  'ExternalService',
] as const
export type EntityKind = (typeof ENTITY_KINDS)[number]

export const RELATIONS = [
  'CONTAINS',
  'RUNS_IN',
  'RUNS_IMAGE',
  'BUILT_FROM',
  'DEPENDS_ON',
  'HAS_VULN',
  'USES',
  'AUTHENTICATES_AS',
  'CAN_READ',
  'CAN_WRITE',
  'CAN_ADMIN',
  'CALLS',
  'USES_MODEL',
  'CONNECTS_TO',
  'RESOLVES_TO',
  'EXPOSED_TO',
] as const
export type Relation = (typeof RELATIONS)[number]

export const SEVERITIES = ['critical', 'high', 'medium', 'low', 'info'] as const
export type Severity = (typeof SEVERITIES)[number]

export const FINDING_STATUSES = ['open', 'resolved'] as const
export type FindingStatus = (typeof FINDING_STATUSES)[number]

export const PERMISSIONS = [
  'graph:read',
  'findings:read',
  'investigate',
  'users:read',
  'users:manage',
  'roles:manage',
  'projects:read',
  'projects:manage',
  'policies:manage',
  'integrations:manage',
] as const
export type FoundationPermission = (typeof PERMISSIONS)[number]

export const ROLE_KEYS = [
  'global_admin',
  'global_reader',
  'project_admin',
  'security_analyst',
  'developer',
  'viewer',
] as const
export type RoleKey = (typeof ROLE_KEYS)[number]

// ---------------------------------------------------------------------------
// Auth (snake_case)
// ---------------------------------------------------------------------------

export interface User {
  id: number
  org_id: number
  email: string
  name: string
  active: boolean
}

export interface Membership {
  role_key: string
  role_name: string
  scope_level: 'global' | 'project'
  project_id?: number
  project_key?: string
}

/** Response of GET /api/auth/me. */
export interface MeResponse {
  user: User
  memberships: Membership[]
  permissions: string[]
}

/** Response of POST /api/auth/login (adds token + expiry). */
export interface LoginResponse extends MeResponse {
  token: string
  expires_at: string
}

/** Normalized principal used across the app (see auth.tsx). */
export interface Principal {
  user: User
  memberships: Membership[]
  permissions: string[]
}

// ---------------------------------------------------------------------------
// Health & stats (snake_case; stats is returned un-enveloped)
// ---------------------------------------------------------------------------

export interface HealthResponse {
  status: string
  graph: boolean
}

export interface Stats {
  raw_events: number
  entities: number
  edges: number
  runs: number
  conflicts: number
  entities_by_kind: Record<string, number>
  edges_by_relation: Record<string, number>
}

// ---------------------------------------------------------------------------
// Graph entities / runs / conflicts (snake_case, except ConflictRow PascalCase)
// ---------------------------------------------------------------------------

export interface EntityRow {
  canonical_id: string
  kind: string
  provider: string
  name: string
  attributes: Record<string, unknown>
  stale: boolean
}

export interface RunRow {
  run_id: string
  source: string
  status: string
  event_count: number
  started_at: string
  finished_at: string
}

/** NOTE: PascalCase — ConflictRow has no json tags on the backend. */
export interface ConflictRow {
  Key: string
  Reason: string
  Detail: string
}

/** Raw Cypher row from GET /api/graph/attack-paths. */
export interface AttackPathRow {
  agent: string
  workload: string
  service_account: string
  cloud_identity: string
  access: string
  datastore: string
}

/** Raw Cypher row from GET /api/graph/ai-calls. */
export interface AiCallRow {
  agent: string | null
  workload: string
  endpoint: string
  ai_service: string
}

// ---------------------------------------------------------------------------
// Findings / intel / evidence / edges (PascalCase — no json tags)
// ---------------------------------------------------------------------------

export interface Finding {
  ID: number
  AssetID: string
  AssetKind: string
  VulnID: string
  IntelRef: string
  RiskScore: number
  Severity: string
  Status: string
  FirstSeen: string
  LastSeen: string
}

export interface EvidenceDetail {
  RawEventID: string
  Source: string
  Collector: string
  RunID: string
  ResourceType: string
  ResourceID: string
}

export interface SemverRange {
  Introduced: string
  Fixed: string
}

export interface AffectedPackage {
  Purl: string
  Ecosystem: string
  Ranges: SemverRange[]
  FixedIn: string[]
}

export interface CVSS {
  Vector: string
  Score: number
  Version: string
}

export interface CanonicalVuln {
  ID: string
  Aliases: string[]
  Summary: string
  Affected: AffectedPackage[]
  CVSS: CVSS
  Severity: string
  KnownExploited: boolean
  EPSS: number
  Published: string
  Modified: string
  Sources: string[]
}

/** Response of GET /api/findings/:id. intel may be null. */
export interface FindingDetailResponse {
  finding: Finding
  intel: CanonicalVuln | null
  evidence: EvidenceDetail[]
}

export interface EdgeDetail {
  ID: number
  SrcID: string
  Relation: string
  DstID: string
  Confidence: number
  Evidence: EvidenceDetail[] | null
}

// ---------------------------------------------------------------------------
// Admin (snake_case)
// ---------------------------------------------------------------------------

export interface PermissionRow {
  key: string
  description: string
}

export interface RoleRow {
  key: string
  name: string
  description?: string
  scope_level?: string
  permissions?: string[]
}

export interface ProjectRow {
  id: number
  key: string
  name: string
}

export interface CreateUserInput {
  email: string
  name: string
  password: string
  role_key: string
  project_ids: number[]
}

// ---------------------------------------------------------------------------
// Connectors (snake_case)
// ---------------------------------------------------------------------------

export type ConnectorStatus =
  'PENDING' | 'VERIFYING' | 'CONNECTED' | 'DISCOVERY_RUNNING' | 'COMPLETE' | 'ERROR'

export type ConnectorScope = 'account' | 'organization'
export type DeploymentMethod = 'cloudformation' | 'terraform' | 'manual'
export type InstallationType = 'standard' | 'outpost'

export interface Connection {
  id: number
  project_id: number
  provider: string
  scope: ConnectorScope
  installation_type: InstallationType
  deployment_method: DeploymentMethod
  account_id: string
  management_account_id?: string
  organizational_unit_id?: string
  parent_connection_id?: number
  role_arn?: string
  external_id: string
  region: string
  capabilities: string[]
  status: ConnectorStatus
  created_at: string
  last_verified_at?: string
}

export interface CapabilityCard {
  name: string
  title: string
  description: string
  default: boolean
}

export interface SetupFile {
  name: string
  content: string
}

export interface Setup {
  role_name: string
  role_arn_template: string
  external_id: string
  scope: string
  deployment_method: string
  trust_policy: Record<string, unknown>
  permission_policy: Record<string, unknown>
  instructions: string[]
  template?: string
  template_format?: string
  launch_url?: string
  template_url?: string
  files?: SetupFile[]
}

export type Outcome = 'PASS' | 'FAIL' | 'NOT_ENABLED'

export interface PermissionResult {
  capability: string
  permission: string
  outcome: Outcome
  detail?: string
}

export interface CapabilityResult {
  capability: string
  outcome: Outcome
  checks?: PermissionResult[]
}

export interface VerifyResult {
  ok: boolean
  capabilities: CapabilityResult[]
  failures?: PermissionResult[]
}

export interface DiscoverySession {
  id: string
  connection_id: number
  run_id: string
  status: 'running' | 'complete' | 'failed' | 'cancelled'
  started_at: string
  ended_at?: string
  error?: string
}

export interface ConnectorStatusView {
  connection_id: number
  status: ConnectorStatus
  latest_session?: DiscoverySession
}

export interface CreateConnectorInput {
  project_id: number
  provider: string
  scope?: ConnectorScope
  installation_type?: InstallationType
  deployment_method?: DeploymentMethod
  account_id?: string
  management_account_id?: string
  organizational_unit_id?: string
  region?: string
  capabilities?: string[]
}
