import { withLatency } from './_helpers'
import type { DashboardData, DailyPoint, OpenedResolvedPoint } from '@/types/dashboard'

// DEMO data for the cybersecurity Dashboard. Values mirror the UI spec and are
// never real customer telemetry. Served through withLatency to simulate a
// network round-trip, matching the other Preview add-on mocks.

// Deterministic pseudo-random walk so the mock renders a believable (but fixed)
// daily curve without pulling in a dependency.
function walk(seed: number, days: number, base: number, drift: number): DailyPoint[] {
  const monthLabel = (i: number) => {
    const start = new Date('2025-11-24T00:00:00Z')
    const d = new Date(start)
    d.setUTCDate(start.getUTCDate() + i)
    return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric', timeZone: 'UTC' })
  }
  let v = base
  let s = seed
  const out: DailyPoint[] = []
  for (let i = 0; i < days; i++) {
    // simple LCG for repeatable jitter
    s = (s * 1103515245 + 12345) & 0x7fffffff
    const jitter = (s / 0x7fffffff - 0.5) * drift
    v = Math.max(0, Math.round(v + jitter + 0.4))
    out.push({ x: monthLabel(i), value: v })
  }
  return out
}

function cumulative(
  openedSeed: number,
  resolvedSeed: number,
  days: number,
): OpenedResolvedPoint[] {
  const opened = walk(openedSeed, days, 300, 26)
  const resolved = walk(resolvedSeed, days, 285, 24)
  let openAcc = 180
  let resAcc = 150
  return opened.map((p, i) => {
    openAcc += Math.round(p.value / 20) + 4
    resAcc += Math.round(resolved[i].value / 20) + 3
    return { x: p.x, opened: openAcc, resolved: resAcc }
  })
}

const DEMO_DASHBOARD: DashboardData = {
  advisories: [
    {
      id: 'adv-1',
      title: 'Critical Post-Auth RCE Vulnerability in n8n',
      published: 'Dec 22, 2025',
      severity: 'critical',
      indicator_count: 0,
    },
    {
      id: 'adv-2',
      title: 'RCE Vulnerability in WatchGuard Firebox Exploited in-the-Wild',
      published: 'Dec 21, 2025',
      severity: 'high',
      indicator_count: 0,
    },
  ],

  open_issues: [
    {
      severity: 'critical',
      letter: 'C',
      count: 62,
      label: 'Critical Issues',
      trend: { pct: -6 },
    },
    { severity: 'high', letter: 'H', count: 61, label: 'High Issues', trend: { pct: 36 } },
    { severity: 'medium', letter: 'M', count: 231, label: 'Medium Issues', trend: { pct: 6 } },
    { severity: 'low', letter: 'L', count: 217, label: 'Low Issues', trend: { pct: 9 } },
  ],

  severity_trends: [
    {
      severity: 'critical',
      label: 'Critical',
      current: 62,
      trend: { pct: -6 },
      series: walk(7, 30, 58, 12),
    },
    {
      severity: 'high',
      label: 'High',
      current: 61,
      trend: { pct: 36 },
      series: walk(23, 30, 44, 14),
    },
  ],

  issue_age: [
    { severity: 'critical', label: 'Critical Issues', days: 22, sla_days: 3 },
    { severity: 'high', label: 'High Issues', days: 20, sla_days: 30 },
    { severity: 'medium', label: 'Medium Issues', days: 27, sla_days: 90 },
    { severity: 'low', label: 'Low Issues', days: 33, sla_days: 180 },
  ],

  security_score: {
    score: 29,
    trend: { pct: 13, increase_is_bad: false },
    source: 'Wiz for Risk Assessment',
    history: walk(42, 29, 16, 6),
  },

  top_issues: [
    {
      id: 'ti-1',
      rule: 'Externally validated Next.js insecure deserialization vulnerability',
      severity: 'critical',
      issues: 14,
      risks: [
        { id: 'r1', label: 'Internet exposed' },
        { id: 'r2', label: 'Remote execution' },
        { id: 'r3', label: 'Production' },
      ],
    },
    {
      id: 'ti-2',
      rule: 'Externally validated software configured with default/weak credentials',
      severity: 'critical',
      issues: 9,
      risks: [
        { id: 'r1', label: 'Internet exposed' },
        { id: 'r2', label: 'Credential access' },
      ],
    },
    {
      id: 'ti-3',
      rule: 'Internet-facing container using an image with initial access vulnerability',
      severity: 'high',
      issues: 23,
      risks: [
        { id: 'r1', label: 'Internet exposed' },
        { id: 'r2', label: 'Container' },
        { id: 'r3', label: 'Initial access' },
      ],
    },
    {
      id: 'ti-4',
      rule: 'Publicly accessible storage bucket containing sensitive data',
      severity: 'high',
      issues: 18,
      risks: [
        { id: 'r1', label: 'Data exposure' },
        { id: 'r2', label: 'Public access' },
      ],
    },
    {
      id: 'ti-5',
      rule: 'Over-privileged IAM role attached to internet-facing workload',
      severity: 'medium',
      issues: 41,
      risks: [
        { id: 'r1', label: 'Lateral movement' },
        { id: 'r2', label: 'Privilege escalation' },
      ],
    },
    {
      id: 'ti-6',
      rule: 'Unpatched OS package with known exploited CVE',
      severity: 'medium',
      issues: 57,
      risks: [
        { id: 'r1', label: 'Known exploited' },
        { id: 'r2', label: 'Patch management' },
      ],
    },
    {
      id: 'ti-7',
      rule: 'Logging disabled on production data store',
      severity: 'low',
      issues: 63,
      risks: [{ id: 'r1', label: 'Observability' }],
    },
  ],

  opened_resolved: cumulative(11, 29, 30),
}

export const dashboardMock = {
  overview: () => withLatency(DEMO_DASHBOARD),
}
