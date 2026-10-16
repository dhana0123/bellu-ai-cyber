import { withLatency } from './_helpers'
import type { Repository, RepositoriesData, SeverityCounts } from '@/types/repositories'

// DEMO data for the Code Repositories page. Never real customer telemetry.

function sev(c: number, h: number, m: number, l: number): SeverityCounts {
  return { critical: c, high: h, medium: m, low: l }
}

const ORGS = ['bellu-labs', 'acme-platform', 'northwind', 'contoso']
const PROJECTS = ['Production', 'Staging', 'Data Platform', 'ML Services', 'Shared']
const PLATFORMS: Repository['platform'][] = ['github', 'gitlab', 'bitbucket', 'azure-devops']

const NAMES = [
  'ai-gateway',
  'model-registry',
  'inference-api',
  'rag-pipeline',
  'agent-orchestrator',
  'prompt-store',
  'vector-index',
  'billing-service',
  'auth-service',
  'web-frontend',
  'terraform-modules',
  'data-ingest',
  'feature-store',
  'eval-harness',
  'guardrails',
  'notebook-tools',
  'embeddings-worker',
  'policy-engine',
  'telemetry-collector',
  'secrets-rotator',
]

function hoursAgoISO(h: number): string {
  return new Date(Date.now() - h * 3600_000).toISOString()
}

// Deterministic pseudo-random so the demo is stable across reloads.
function rng(seed: number): () => number {
  let s = seed
  return () => {
    s = (s * 1664525 + 1013904223) % 4294967296
    return s / 4294967296
  }
}

const rand = rng(42)
const pick = <T>(arr: T[]) => arr[Math.floor(rand() * arr.length)]
const int = (max: number) => Math.floor(rand() * (max + 1))

const REPOS: Repository[] = NAMES.flatMap((base, i) => {
  // A couple of orgs each to reach a realistic count.
  return [0, 1].map((k) => {
    const org = ORGS[(i + k) % ORGS.length]
    return {
      id: `repo-${i}-${k}`,
      name: `${org}/${base}`,
      organization: org,
      platform: pick(PLATFORMS),
      project: pick(PROJECTS),
      visibility: rand() > 0.55 ? 'private' : ('public' as const),
      scanning_enabled: rand() > 0.1,
      issues: sev(int(2), int(4), int(3), 0),
      findings: sev(int(30), int(220), int(260), int(60)),
      policies: 1 + int(4),
      project_count: 1 + int(5),
      last_scan: hoursAgoISO(int(240)),
    }
  })
})

// Aggregate finding categories (right-side breakdown), summed across repos but
// shown as representative demo totals.
const DATA: RepositoriesData = {
  total: REPOS.length,
  repositories: REPOS,
  finding_categories: [
    { id: 'vuln', label: 'Vulnerability Findings', counts: sev(20, 105, 204, 40) },
    { id: 'secret', label: 'Secret Findings', counts: sev(0, 8, 0, 0) },
    { id: 'data', label: 'Data Findings', counts: sev(1, 0, 1, 0) },
    { id: 'iac', label: 'IaC Findings', counts: sev(0, 0, 18, 13) },
    { id: 'sast', label: 'SAST Findings', counts: sev(0, 4, 0, 0) },
    { id: 'ai-sast', label: 'AI SAST Findings', counts: sev(0, 0, 0, 0) },
    { id: 'swmgmt', label: 'Software Management Findings', counts: sev(0, 0, 78, 0) },
  ],
}

export const repositoriesMock = {
  overview: () => withLatency(DATA),
}
