// Types for the Code Repositories page (Wiz-style). DEMO data only
// (src/mocks/repositories.ts); no backend yet. snake_case mirrors the backend's
// prevailing convention so a real API can slot in later.

export type RepoVisibility = 'public' | 'private'

/** Per-severity counts (critical / high / medium / low). */
export interface SeverityCounts {
  critical: number
  high: number
  medium: number
  low: number
}

/** One finding category shown in the right-side breakdown panel. */
export interface FindingCategory {
  id: string
  label: string
  counts: SeverityCounts
}

/** One row in the repositories table. */
export interface Repository {
  id: string
  name: string
  organization: string
  platform: 'github' | 'gitlab' | 'bitbucket' | 'azure-devops'
  project: string
  visibility: RepoVisibility
  /** Whether scan coverage is enabled (shown as the Scan Coverage toggle). */
  scanning_enabled: boolean
  /** Open issues on the repo. */
  issues: SeverityCounts
  /** Total findings across all categories. */
  findings: SeverityCounts
  /** Number of policies applied. */
  policies: number
  /** Number of projects this repo maps to. */
  project_count: number
  /** ISO timestamp of the last scan. */
  last_scan: string
}

export interface RepositoriesData {
  total: number
  repositories: Repository[]
  /** Aggregated finding categories for the right-side breakdown panel. */
  finding_categories: FindingCategory[]
}
