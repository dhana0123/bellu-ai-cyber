// Real-data adapter for the Risk Issues page. It fetches real findings from the
// backend (GET /api/findings) and maps them into the IssuesData shape the
// existing UI already renders — so the page keeps its exact layout (analytics
// cards + vulnerability grid) but shows real correlated findings instead of
// mock demo data.
//
// The backend exposes individual findings, not pre-aggregated trend series.
// Severity metrics are tallied for real from the findings; the two time-series
// cards (Open/Resolved, Open Issues) are given empty series so their chart
// components render unchanged without fabricating history.
import { realApi } from '@/lib/api'
import type { Finding as BackendFinding, Severity } from '@/types/foundation'
import type {
  IssuesData,
  Issue,
  IssueStatus,
  SeverityMetric,
  OpenResolvedPoint,
  OpenIssuesPoint,
} from '@/types/issues'

const KIND_LABEL: Record<string, string> = {
  Package: 'Package',
  ContainerImage: 'Container Image',
  AIModel: 'AI Model',
  AIAgent: 'AI Agent',
  AIService: 'AI Service',
  MCPServer: 'MCP Server',
  DataStore: 'Data Store',
  Compute: 'Compute',
  Workload: 'Workload',
  Identity: 'Identity',
}

function labelFor(kind: string): string {
  return KIND_LABEL[kind] ?? kind
}

function severityOf(s: string): Severity {
  const v = (s || '').toLowerCase()
  if (v === 'critical' || v === 'high' || v === 'medium' || v === 'low') return v
  return 'info'
}

// Backend finding status is open|resolved; the grid view-model adds in_progress
// which the backend doesn't model, so we map open -> open, resolved -> resolved.
function statusFor(s: string): IssueStatus {
  return s === 'resolved' ? 'resolved' : 'open'
}

function shortName(assetID: string): string {
  const parts = assetID.split(':')
  const tail = parts[parts.length - 1] || assetID
  const slash = tail.split('/')
  return slash[slash.length - 1] || tail
}

// Internet-facing assets get the red globe glyph. Network endpoints / external
// services are the reachable ones in the graph model.
function isInternetFacing(kind: string): boolean {
  return kind === 'NetworkEndpoint' || kind === 'ExternalService'
}

// toIssue maps a backend finding into a grid row.
function toIssue(f: BackendFinding): Issue {
  const sev = severityOf(f.Severity)
  return {
    id: String(f.ID),
    title: `${f.VulnID} affecting ${shortName(f.AssetID)}`,
    severity: sev,
    status: statusFor(f.Status),
    resource: f.AssetID,
    internet_facing: isInternetFacing(f.AssetKind),
    ticket: null,
    // One risk tag per finding (the cited vuln) so the Risks column renders.
    risks: [{ id: f.VulnID, label: f.VulnID }],
    project_count: 1,
    subscription: labelFor(f.AssetKind),
    created: f.FirstSeen ? new Date(f.FirstSeen).toLocaleDateString() : '—',
  }
}

const SEVERITY_META: Array<{ severity: Severity; letter: string; label: string }> = [
  { severity: 'critical', letter: 'C', label: 'Critical' },
  { severity: 'high', letter: 'H', label: 'High' },
  { severity: 'medium', letter: 'M', label: 'Medium' },
  { severity: 'low', letter: 'L', label: 'Low' },
]

// severityMetrics tallies real counts per severity bucket for the Issue
// Severity card. trend_pct is 0 (neutral chip) since there's no history yet.
function severityMetrics(findings: BackendFinding[]): SeverityMetric[] {
  const counts = new Map<Severity, number>()
  for (const f of findings) {
    const s = severityOf(f.Severity)
    counts.set(s, (counts.get(s) ?? 0) + 1)
  }
  return SEVERITY_META.map((m) => ({
    severity: m.severity,
    letter: m.letter,
    label: m.label,
    count: counts.get(m.severity) ?? 0,
    trend_pct: 0,
  }))
}

// toIssuesData maps real findings into the page's data shape.
export function toIssuesData(findings: BackendFinding[]): IssuesData {
  const open_resolved: OpenResolvedPoint[] = []
  const open_issues_trend: OpenIssuesPoint[] = []
  return {
    severity_metrics: severityMetrics(findings),
    open_resolved,
    open_issues_trend,
    open_issues_trend_pct: 0,
    issues: findings.map(toIssue),
  }
}

// fetchIssues loads real findings and maps them to IssuesData.
export async function fetchIssues(): Promise<IssuesData> {
  const { findings } = await realApi.findings({ limit: 500 })
  return toIssuesData(findings ?? [])
}
