// TypeScript shapes for the Vulnerabilities (Findings inventory) page. DEMO data
// only (src/mocks/vulnerabilities.ts); no backend. snake_case mirrors the
// backend's prevailing convention.

import type { Severity } from './foundation'

export type FindingStatus = 'unresolved' | 'resolved' | 'in_progress'

/** A compact critical/high count pair shown as C/H badges. */
export interface SeverityCount {
  critical: number
  high: number
}

/** One row in the "Top Exploitable Components" card. */
export interface ExploitableComponent {
  id: string
  name: string
  version: string
  counts: SeverityCount
}

/** One row in the "Top Vulnerable Resources" card. */
export interface VulnerableResource {
  id: string
  name: string
  counts: SeverityCount
}

/** One row in the main findings inventory table. */
export interface Finding {
  id: string
  /** Resource path, e.g. docker.io/vulhub/grafana. */
  resource: string
  /** Object-type tag under the resource, e.g. "Container Image". */
  resource_type: string
  /** Component/service name, e.g. "Grafana". */
  component: string
  /** Technology tag, e.g. "Hosted Technology". */
  component_tech: string
  status: FindingStatus
  severity: Severity
  /** Cascading related risk-issue counts. */
  related: SeverityCount
  /** Remediation target version string. */
  fix_version: string
  /** Detection source vector, e.g. "File path" or "Package". */
  detection_method: string
  /** Origin platform/subscription string. */
  subscription: string
}

export interface VulnerabilitiesData {
  total: number
  top_components: ExploitableComponent[]
  top_resources: VulnerableResource[]
  findings: Finding[]
}
