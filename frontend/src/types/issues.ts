// TypeScript shapes for the Risk Issues page. DEMO data only (src/mocks/issues.ts);
// no backend. snake_case mirrors the backend's prevailing convention.

import type { Severity } from './foundation'

export type IssueStatus = 'open' | 'in_progress' | 'resolved'

/** A risk icon tag shown in the grid "Risks" column. */
export interface IssueRiskTag {
  id: string
  label: string
}

/** One row in the vulnerability grid. */
export interface Issue {
  id: string
  title: string
  severity: Severity
  status: IssueStatus
  /** Affected resource (endpoint/url/arn). */
  resource: string
  /** True when the resource is internet-facing (red globe glyph). */
  internet_facing: boolean
  /** Ticket reference, or null when unticketed. */
  ticket: string | null
  risks: IssueRiskTag[]
  project_count: number
  subscription: string
  created: string
}

/** A severity metric row in the "Issue Severity" card. */
export interface SeverityMetric {
  severity: Severity
  letter: string
  count: number
  label: string
  /** Signed percentage change; 0 renders as a neutral chip. */
  trend_pct: number
}

/** A point on the Open/Resolved trend chart. */
export interface OpenResolvedPoint {
  /** Date label, e.g. "Dec 15". */
  x: string
  opened: number
  resolved: number
  /** Extra tooltip-only metric per the spec. */
  new_resource: number
  /** Day-over-day deltas surfaced in the custom tooltip. */
  opened_change: number
  resolved_change: number
}

/** A point on the Open Issues (stepped) trend chart. */
export interface OpenIssuesPoint {
  x: string
  critical: number
  high: number
}

export interface IssuesData {
  severity_metrics: SeverityMetric[]
  open_resolved: OpenResolvedPoint[]
  open_issues_trend: OpenIssuesPoint[]
  /** Headline trend for the Open Issues card chip. */
  open_issues_trend_pct: number
  issues: Issue[]
}
