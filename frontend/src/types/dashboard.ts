// TypeScript shapes for the cybersecurity Dashboard page. This module has NO
// backend yet — the mock layer in src/mocks/dashboard.ts implements these shapes
// with DEMO values. snake_case mirrors the backend's prevailing convention.

import type { Severity } from './foundation'

/** A published security advisory shown in the top banner. */
export interface Advisory {
  id: string
  title: string
  /** Human-readable publish date, e.g. "Dec 22, 2025". */
  published: string
  severity: Severity
  /** Right-aligned indicator count badge. */
  indicator_count: number
}

/** Trend direction + magnitude for a metric chip. */
export interface Trend {
  /** Signed percentage; sign decides the arrow and chip color intent. */
  pct: number
  /** When true, an increase is bad (red up / green down). Defaults to true. */
  increase_is_bad?: boolean
}

/** One row in the "Open Issues" card. */
export interface SeverityMetric {
  severity: Severity
  /** Short letter indicator, e.g. "C", "H", "M", "L". */
  letter: string
  count: number
  label: string
  trend: Trend
}

/** A daily point used by the severity mini area charts. */
export interface DailyPoint {
  /** Day label, e.g. "Dec 1". */
  x: string
  value: number
}

/** A labelled severity trend used by the "Issues by Severity" mini charts. */
export interface SeverityTrend {
  severity: Severity
  label: string
  current: number
  trend: Trend
  series: DailyPoint[]
}

/** A semi-circular gauge in the "Average Issue Age" card. */
export interface IssueAgeGauge {
  severity: Severity
  label: string
  /** Current average age in days. */
  days: number
  /** Target SLA in days. */
  sla_days: number
}

/** The "Security Score" card payload. */
export interface SecurityScore {
  /** 0..100 posture score. */
  score: number
  trend: Trend
  source: string
  /** Historical score points for the supporting area chart. */
  history: DailyPoint[]
}

/** A risk tag shown as a small chip in the Top Issues table. */
export interface IssueRisk {
  id: string
  label: string
}

/** One row in the "Top Issues" table. */
export interface TopIssue {
  id: string
  rule: string
  severity: Severity
  issues: number
  risks: IssueRisk[]
}

/** A cumulative opened/resolved point for the burn-up chart. */
export interface OpenedResolvedPoint {
  /** Date label, e.g. "Nov 24". */
  x: string
  opened: number
  resolved: number
}

/** Full dashboard payload returned by the mock client. */
export interface DashboardData {
  advisories: Advisory[]
  open_issues: SeverityMetric[]
  severity_trends: SeverityTrend[]
  issue_age: IssueAgeGauge[]
  security_score: SecurityScore
  top_issues: TopIssue[]
  opened_resolved: OpenedResolvedPoint[]
}
