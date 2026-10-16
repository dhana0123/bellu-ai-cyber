// Real-data adapter for the Vulnerabilities (Findings inventory) page. It
// fetches real findings from the backend (GET /api/findings) and maps them into
// the VulnerabilitiesData shape the existing UI already renders — so the page
// keeps its exact layout (analytics cards + findings table) but shows real
// correlated findings instead of mock demo data.
import { realApi } from '@/lib/api'
import type { Finding as BackendFinding, Severity } from '@/types/foundation'
import type {
  VulnerabilitiesData,
  Finding,
  FindingStatus,
  ExploitableComponent,
  VulnerableResource,
  SeverityCount,
} from '@/types/vulnerabilities'

// Human-readable label for an asset kind, used as the resource-type tag.
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

// The backend finding status is open|resolved; the table view-model uses
// unresolved|resolved|in_progress. Map open -> unresolved.
function statusFor(s: string): FindingStatus {
  return s === 'resolved' ? 'resolved' : 'unresolved'
}

function severityOf(s: string): Severity {
  const v = (s || '').toLowerCase()
  if (v === 'critical' || v === 'high' || v === 'medium' || v === 'low') return v
  return 'info'
}

// shortName trims a canonical asset id down to a readable component name.
function shortName(assetID: string): string {
  const parts = assetID.split(':')
  const tail = parts[parts.length - 1] || assetID
  // For purls / arns keep the last meaningful segment.
  const slash = tail.split('/')
  return slash[slash.length - 1] || tail
}

// toFinding maps a backend finding into the table view-model row.
function toFinding(f: BackendFinding): Finding {
  const sev = severityOf(f.Severity)
  return {
    id: String(f.ID),
    resource: f.AssetID,
    resource_type: labelFor(f.AssetKind),
    component: shortName(f.AssetID),
    component_tech: labelFor(f.AssetKind),
    status: statusFor(f.Status),
    severity: sev,
    // "Related risk issues" aren't modeled per-finding yet; surface this
    // finding's own severity as a 1-count so the badge isn't empty.
    related: {
      critical: sev === 'critical' ? 1 : 0,
      high: sev === 'high' ? 1 : 0,
    },
    fix_version: '—',
    detection_method: f.IntelRef ? 'Intel match' : 'Correlation',
    subscription: f.VulnID,
  }
}

// addCount accumulates a severity into a critical/high count pair.
function addCount(c: SeverityCount, sev: Severity): SeverityCount {
  return {
    critical: c.critical + (sev === 'critical' ? 1 : 0),
    high: c.high + (sev === 'high' ? 1 : 0),
  }
}

// topBy groups findings by a key, tallies critical/high counts, and returns the
// top N groups by total critical+high, descending.
function topBy(
  findings: BackendFinding[],
  key: (f: BackendFinding) => string,
  n: number,
): Array<{ id: string; counts: SeverityCount }> {
  const m = new Map<string, SeverityCount>()
  for (const f of findings) {
    const k = key(f)
    if (!k) continue
    m.set(k, addCount(m.get(k) ?? { critical: 0, high: 0 }, severityOf(f.Severity)))
  }
  return [...m.entries()]
    .map(([id, counts]) => ({ id, counts }))
    .sort((a, b) => b.counts.critical + b.counts.high - (a.counts.critical + a.counts.high))
    .slice(0, n)
}

// toVulnerabilitiesData maps real findings into the page's data shape. The two
// analytics cards are derived from the findings themselves: "Top Exploitable
// Components" groups by the cited vulnerability (VulnID), "Top Vulnerable
// Resources" groups by the affected asset.
export function toVulnerabilitiesData(findings: BackendFinding[]): VulnerabilitiesData {
  const top_components: ExploitableComponent[] = topBy(findings, (f) => f.VulnID, 6).map((g) => ({
    id: g.id,
    name: g.id,
    version: '—',
    counts: g.counts,
  }))

  const top_resources: VulnerableResource[] = topBy(findings, (f) => f.AssetID, 6).map((g) => ({
    id: g.id,
    name: shortName(g.id),
    counts: g.counts,
  }))

  return {
    total: findings.length,
    top_components,
    top_resources,
    findings: findings.map(toFinding),
  }
}

// fetchVulnerabilities loads real findings and maps them to VulnerabilitiesData.
export async function fetchVulnerabilities(): Promise<VulnerabilitiesData> {
  const { findings } = await realApi.findings({ limit: 500 })
  return toVulnerabilitiesData(findings ?? [])
}
