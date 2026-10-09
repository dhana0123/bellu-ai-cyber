// TypeScript shapes for the SEVEN add-on modules. These modules have NO backend
// yet — the shapes here are the *proposed* API contracts (documented in
// .kiro/specs/bellu-ui-ux/07-api-contracts.md) that the mock layer implements
// and a future backend would serve. snake_case, mirroring the real backend's
// prevailing convention.

import type { Severity } from './foundation'

// Proposed "Preview" permission strings for the add-on modules. Documented as
// future backend additions; granted to the dev mock principal so Preview
// sections render.
export const ADDON_PERMISSIONS = [
  'gateway:read',
  'runtime:read',
  'agents:read',
  'data:read',
  'supplychain:read',
  'media:read',
  'governance:read',
] as const
export type AddonPermission = (typeof ADDON_PERMISSIONS)[number]

// ---------------------------------------------------------------------------
// AI Gateway
// ---------------------------------------------------------------------------

export interface GatewayStats {
  total_calls_24h: number
  avg_latency_ms: number
  error_rate_pct: number
  active_instances: number
  failover_events_24h: number
  cache_hit_rate_pct: number
}

export interface GatewayTimePoint {
  x: string // bucket label (hour/day)
  openai: number
  anthropic: number
  local: number
  // index signature so it satisfies the chart wrapper's SeriesDatum shape
  [series: string]: string | number
}

export interface RoutingPolicy {
  id: string
  name: string
  source: string // app/team
  model_match: string // model name / glob
  destination: string // provider key
  priority: number
  enabled: boolean
}

export interface ApiKey {
  id: string
  name: string
  owner: string
  monthly_budget_usd: number
  spent_usd: number
  rate_limit_rpm: number
  active: boolean
  created_at: string
}

export interface GatewayLogEntry {
  id: string
  ts: string
  api_key: string
  provider: string
  prompt_tokens: number
  latency_ms: number
  status: 'success' | 'blocked' | 'error'
}

// ---------------------------------------------------------------------------
// Runtime Defense
// ---------------------------------------------------------------------------

export interface RuntimeAlert {
  id: string
  ts: string
  severity: Severity
  agent: string
  event_type: 'prompt_injection' | 'data_exfil' | 'policy_violation' | 'jailbreak'
  summary: string
  status: 'new' | 'acknowledged' | 'resolved'
  prompt_excerpt: string
  response_excerpt: string
}

export interface GuardrailRule {
  id: string
  name: string
  condition: string
  action: 'allow' | 'deny' | 'notify'
  hits_24h: number
  enabled: boolean
}

export interface ChatMonitorMessage {
  id: string
  ts: string
  session_id: string
  role: 'user' | 'assistant'
  text: string
  flagged: boolean
}

// ---------------------------------------------------------------------------
// Agent Security
// ---------------------------------------------------------------------------

export interface Agent {
  id: string
  name: string
  creator: string
  status: 'active' | 'idle' | 'revoked'
  last_seen: string
  actions_24h: number
  risk_score: number
  allowed_tools: string[]
}

export interface ToolAuthorization {
  id: string
  tool: string
  scope: string
  agents: number
  allowed: boolean
}

export interface AgentAuditEntry {
  id: string
  ts: string
  agent: string
  tool: string
  action: string
  outcome: 'allowed' | 'blocked'
}

// ---------------------------------------------------------------------------
// AI Data Security
// ---------------------------------------------------------------------------

export interface DataAsset {
  id: string
  name: string
  data_type: 'PII' | 'PHI' | 'secret' | 'IP' | 'financial'
  location: string
  owning_team: string
  sensitivity: Severity
}

export interface LineageFlow {
  id: string
  source: string
  via: string // model/agent
  destination: string
  policy_violation: boolean
}

export interface DlpPolicy {
  id: string
  name: string
  pattern: string
  category: string
  action: 'block' | 'warn' | 'log'
  matches_24h: number
  enabled: boolean
}

// ---------------------------------------------------------------------------
// AI Supply Chain
// ---------------------------------------------------------------------------

export interface ModelRegistryEntry {
  id: string
  name: string
  framework: string
  hash: string
  source: 'internal' | 'huggingface' | 'vendor'
  criticality: Severity
  signed: boolean
}

export interface ScanResult {
  id: string
  artifact: string
  component: string
  cve: string
  severity: Severity
  fixed_version?: string
}

export interface ProvenanceRecord {
  id: string
  model: string
  built_by: string
  built_at: string
  dataset_ref: string
  signature_verified: boolean
}

// ---------------------------------------------------------------------------
// Media Authenticity
// ---------------------------------------------------------------------------

export interface MediaItem {
  id: string
  kind: 'audio' | 'video' | 'image'
  source: string
  submitted_by: string
  ts: string
  authenticity_score: number // 0..100, higher = more authentic
  confidence_pct: number
  status: 'queued' | 'flagged' | 'approved'
  segments: MediaSegment[]
}

export interface MediaSegment {
  start_s: number
  end_s: number
  label: string // e.g. "voice synthesis artifact"
  suspicious: boolean
}

export interface MediaIntegration {
  id: string
  name: string // e.g. Twilio, Zoom
  status: 'connected' | 'error' | 'disabled'
  scanned_24h: number
}

// ---------------------------------------------------------------------------
// Governance & Compliance
// ---------------------------------------------------------------------------

export interface ComplianceFramework {
  id: string
  name: string // e.g. "EU AI Act", "SOC 2", "HIPAA"
  controls_total: number
  controls_passing: number
}

export interface GovernancePolicy {
  id: string
  name: string
  category: string
  applies_to: string
  enabled: boolean
}

export interface AuditLogEntry {
  id: string
  ts: string
  actor: string
  module: string
  action: string
  target: string
}
