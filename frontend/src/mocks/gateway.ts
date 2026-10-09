import { withLatency } from './_helpers'
import type {
  ApiKey,
  GatewayLogEntry,
  GatewayStats,
  GatewayTimePoint,
  RoutingPolicy,
} from '@/types/addons'

const DEMO_GATEWAY_STATS: GatewayStats = {
  total_calls_24h: 48213,
  avg_latency_ms: 182,
  error_rate_pct: 0.6,
  active_instances: 3,
  failover_events_24h: 2,
  cache_hit_rate_pct: 37.4,
}

const DEMO_GATEWAY_SERIES: GatewayTimePoint[] = [
  { x: '00:00', openai: 1200, anthropic: 640, local: 210 },
  { x: '04:00', openai: 980, anthropic: 520, local: 180 },
  { x: '08:00', openai: 2100, anthropic: 1180, local: 420 },
  { x: '12:00', openai: 2680, anthropic: 1420, local: 510 },
  { x: '16:00', openai: 2440, anthropic: 1320, local: 480 },
  { x: '20:00', openai: 1760, anthropic: 980, local: 360 },
]

const DEMO_ROUTING_POLICIES: RoutingPolicy[] = [
  {
    id: 'rp-001',
    name: 'Prod chat → GPT-4o',
    source: 'web-app',
    model_match: 'gpt-4o*',
    destination: 'openai-prod',
    priority: 1,
    enabled: true,
  },
  {
    id: 'rp-002',
    name: 'Internal tools → Claude',
    source: 'internal-tools',
    model_match: 'claude-3*',
    destination: 'anthropic-prod',
    priority: 2,
    enabled: true,
  },
  {
    id: 'rp-003',
    name: 'Batch jobs → local',
    source: 'batch',
    model_match: '*',
    destination: 'local-llama',
    priority: 3,
    enabled: false,
  },
]

const DEMO_API_KEYS: ApiKey[] = [
  {
    id: 'key-prod',
    name: 'prod-web',
    owner: 'platform-team',
    monthly_budget_usd: 5000,
    spent_usd: 2140,
    rate_limit_rpm: 600,
    active: true,
    created_at: '2026-08-01T10:00:00Z',
  },
  {
    id: 'key-data',
    name: 'data-science',
    owner: 'ml-team',
    monthly_budget_usd: 2000,
    spent_usd: 1890,
    rate_limit_rpm: 120,
    active: true,
    created_at: '2026-09-12T09:30:00Z',
  },
  {
    id: 'key-sandbox',
    name: 'sandbox',
    owner: 'dev',
    monthly_budget_usd: 250,
    spent_usd: 44,
    rate_limit_rpm: 60,
    active: false,
    created_at: '2026-09-28T14:15:00Z',
  },
]

const DEMO_LOGS: GatewayLogEntry[] = [
  {
    id: 'log-1',
    ts: '2026-10-09T11:59:02Z',
    api_key: 'prod-web',
    provider: 'openai',
    prompt_tokens: 820,
    latency_ms: 160,
    status: 'success',
  },
  {
    id: 'log-2',
    ts: '2026-10-09T11:58:40Z',
    api_key: 'data-science',
    provider: 'anthropic',
    prompt_tokens: 1340,
    latency_ms: 240,
    status: 'success',
  },
  {
    id: 'log-3',
    ts: '2026-10-09T11:58:11Z',
    api_key: 'prod-web',
    provider: 'openai',
    prompt_tokens: 2100,
    latency_ms: 980,
    status: 'blocked',
  },
  {
    id: 'log-4',
    ts: '2026-10-09T11:57:55Z',
    api_key: 'sandbox',
    provider: 'local',
    prompt_tokens: 320,
    latency_ms: 70,
    status: 'error',
  },
]

export const gatewayMock = {
  stats: () => withLatency(DEMO_GATEWAY_STATS),
  series: () => withLatency(DEMO_GATEWAY_SERIES),
  policies: () => withLatency(DEMO_ROUTING_POLICIES),
  keys: () => withLatency(DEMO_API_KEYS),
  logs: () => withLatency(DEMO_LOGS),
}
