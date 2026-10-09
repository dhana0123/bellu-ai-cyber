import { withLatency } from './_helpers'
import type { Agent, AgentAuditEntry, ToolAuthorization } from '@/types/addons'

const DEMO_AGENTS: Agent[] = [
  {
    id: 'ag-01',
    name: 'support-bot',
    creator: 'platform-team',
    status: 'active',
    last_seen: '2026-10-09T11:58:00Z',
    actions_24h: 412,
    risk_score: 28,
    allowed_tools: ['search', 'crm-read', 'email-draft'],
  },
  {
    id: 'ag-02',
    name: 'sales-assistant',
    creator: 'sales-ops',
    status: 'active',
    last_seen: '2026-10-09T11:40:00Z',
    actions_24h: 188,
    risk_score: 61,
    allowed_tools: ['crm-read', 'crm-write', 'calendar'],
  },
  {
    id: 'ag-03',
    name: 'legacy-scraper',
    creator: 'unknown',
    status: 'revoked',
    last_seen: '2026-09-30T03:12:00Z',
    actions_24h: 0,
    risk_score: 88,
    allowed_tools: ['web-fetch'],
  },
]

const DEMO_TOOLS: ToolAuthorization[] = [
  { id: 't1', tool: 'crm-read', scope: 'read:contacts', agents: 2, allowed: true },
  { id: 't2', tool: 'crm-write', scope: 'write:contacts', agents: 1, allowed: true },
  { id: 't3', tool: 'web-fetch', scope: 'net:egress', agents: 1, allowed: false },
  { id: 't4', tool: 'shell-exec', scope: 'os:exec', agents: 0, allowed: false },
]

const DEMO_AUDIT: AgentAuditEntry[] = [
  {
    id: 'au-1',
    ts: '2026-10-09T11:58:02Z',
    agent: 'support-bot',
    tool: 'crm-read',
    action: 'lookup contact #4821',
    outcome: 'allowed',
  },
  {
    id: 'au-2',
    ts: '2026-10-09T11:50:14Z',
    agent: 'sales-assistant',
    tool: 'crm-write',
    action: 'update opportunity stage',
    outcome: 'allowed',
  },
  {
    id: 'au-3',
    ts: '2026-10-08T22:03:51Z',
    agent: 'legacy-scraper',
    tool: 'web-fetch',
    action: 'GET external endpoint',
    outcome: 'blocked',
  },
]

export const agentsMock = {
  list: () => withLatency(DEMO_AGENTS),
  tools: () => withLatency(DEMO_TOOLS),
  audit: () => withLatency(DEMO_AUDIT),
}
