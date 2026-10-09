import { withLatency } from './_helpers'
import type { AuditLogEntry, ComplianceFramework, GovernancePolicy } from '@/types/addons'

const DEMO_FRAMEWORKS: ComplianceFramework[] = [
  { id: 'fw-1', name: 'EU AI Act', controls_total: 42, controls_passing: 31 },
  { id: 'fw-2', name: 'SOC 2', controls_total: 64, controls_passing: 58 },
  { id: 'fw-3', name: 'HIPAA', controls_total: 28, controls_passing: 20 },
  { id: 'fw-4', name: 'NIST AI RMF', controls_total: 36, controls_passing: 24 },
]

const DEMO_POLICIES: GovernancePolicy[] = [
  {
    id: 'gp-1',
    name: 'Data residency: EU only',
    category: 'data',
    applies_to: 'all projects',
    enabled: true,
  },
  {
    id: 'gp-2',
    name: 'Red-team before prod',
    category: 'lifecycle',
    applies_to: 'customer-facing models',
    enabled: true,
  },
  {
    id: 'gp-3',
    name: 'Human review for high-risk outputs',
    category: 'oversight',
    applies_to: 'agents',
    enabled: false,
  },
]

const DEMO_AUDIT: AuditLogEntry[] = [
  {
    id: 'g-1',
    ts: '2026-10-09T11:20:00Z',
    actor: 'admin@demo',
    module: 'Gateway',
    action: 'disabled policy',
    target: 'Batch jobs → local',
  },
  {
    id: 'g-2',
    ts: '2026-10-09T09:02:00Z',
    actor: 'analyst@demo',
    module: 'Supply Chain',
    action: 'approved model',
    target: 'recommender-v3',
  },
  {
    id: 'g-3',
    ts: '2026-10-08T18:44:00Z',
    actor: 'ci-pipeline',
    module: 'Agents',
    action: 'revoked agent',
    target: 'legacy-scraper',
  },
]

export const governanceMock = {
  frameworks: () => withLatency(DEMO_FRAMEWORKS),
  policies: () => withLatency(DEMO_POLICIES),
  audit: () => withLatency(DEMO_AUDIT),
}
