// DEMO fixtures for the Finding Detail page (features/issues/FindingDetail.tsx).
//
// These mirror the extended `GET /api/findings/:id` shape (FindingDetailResponse
// with `reports` + `triggered_by`). They cover the four render paths the page
// must handle: red-only, blue-only, both, and no-report (graceful fallback).
//
// SAFETY (Req 16.3 / 18.3 — P6): the weaponized `poc_script_code` body is NEVER
// transported. `poc_available` is a flag only, and there is no field anywhere in
// these fixtures (or in the FindingReportView type) that carries a PoC script
// body. `sections` carries only descriptive long-form fields.
import { withLatency } from './_helpers'
import type {
  CanonicalVuln,
  EvidenceDetail,
  Finding,
  FindingDetailResponse,
  FindingReportView,
} from '@/types/foundation'

const DEMO_FINDING: Finding = {
  ID: 4201,
  AssetID: 'repo:acme/payments-api',
  AssetKind: 'Repository',
  VulnID: 'CVE-2024-0001',
  IntelRef: 'intel:CVE-2024-0001',
  RiskScore: 88,
  Severity: 'critical',
  Status: 'Unvalidated',
  FirstSeen: '2024-12-18T10:04:00Z',
  LastSeen: '2024-12-20T09:12:00Z',
}

const DEMO_INTEL: CanonicalVuln = {
  ID: 'CVE-2024-0001',
  Aliases: ['GHSA-xxxx-yyyy-zzzz'],
  Summary: 'Unauthenticated remote code execution in the request parser.',
  Affected: [
    {
      Purl: 'pkg:npm/request-parser',
      Ecosystem: 'npm',
      Ranges: [{ Introduced: '1.0.0', Fixed: '1.4.2' }],
      FixedIn: ['1.4.2'],
    },
  ],
  CVSS: { Vector: 'CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H', Score: 9.8, Version: '3.1' },
  Severity: 'critical',
  KnownExploited: true,
  EPSS: 0.812,
  Published: '2024-12-01T00:00:00Z',
  Modified: '2024-12-15T00:00:00Z',
  Sources: ['nvd', 'ghsa'],
}

const DEMO_EVIDENCE: EvidenceDetail[] = [
  {
    RawEventID: 'raw-9f3c1a7b2d4e',
    Source: 'github',
    Collector: 'repo-scan',
    RunID: 'run-77f2',
    ResourceType: 'Repository',
    ResourceID: 'acme/payments-api',
  },
]

// Red-team report: what was found. `sections` carries description/impact/analysis
// and a poc_description — but NEVER the weaponized script body.
const RED_REPORT: FindingReportView = {
  team: 'red',
  title: 'Request parser RCE reachable from the public ingress',
  severity: 'critical',
  poc_available: true,
  sections: {
    description: 'A crafted request body reaches the vulnerable parser before auth.',
    impact: 'Full remote code execution as the service account.',
    technical_analysis:
      'The parser deserializes attacker-controlled input prior to the auth middleware.',
    evidence: ['Reached the sink from the /ingest route with no auth header.'],
    poc_description: 'A single crafted POST to /ingest triggers the sink (body withheld).',
    code_locations: ['src/parser/index.ts:142', 'src/routes/ingest.ts:31'],
    fix_diffs: ['--- a/src/parser/index.ts\n+++ b/src/parser/index.ts\n@@ guard added'],
  },
  rendered_markdown:
    '## Red Team\nRequest parser RCE reachable from the public ingress.\n\n**Impact:** RCE.',
}

// Blue-team report: remediation. `sections` carries remediation/effort/verification.
const BLUE_REPORT: FindingReportView = {
  team: 'blue',
  title: 'Remediation: upgrade request-parser and gate the ingest route',
  severity: 'critical',
  poc_available: false,
  sections: {
    remediation_steps: [
      'Upgrade request-parser to 1.4.2 or later.',
      'Move the auth middleware ahead of the parser.',
    ],
    fix_effort: 'low',
    fix_verification: 'Re-run the scan; the sink is no longer reachable pre-auth.',
    update_history: [
      { at: '2024-12-19T08:00:00Z', note: 'Triaged and assigned to platform team.' },
      { at: '2024-12-20T09:12:00Z', note: 'Fix PR opened.' },
    ],
  },
  rendered_markdown:
    '## Blue Team\nUpgrade request-parser and gate the ingest route.\n\n**Effort:** low.',
}

function base(reports: FindingReportView[]): FindingDetailResponse {
  return {
    finding: DEMO_FINDING,
    intel: DEMO_INTEL,
    evidence: DEMO_EVIDENCE,
    reports,
    triggered_by: '90210',
  }
}

/** Both red and blue reports present. */
export const findingDetailBoth: FindingDetailResponse = base([RED_REPORT, BLUE_REPORT])

/** Only the red-team report present. */
export const findingDetailRedOnly: FindingDetailResponse = base([RED_REPORT])

/** Only the blue-team report present. */
export const findingDetailBlueOnly: FindingDetailResponse = base([BLUE_REPORT])

/** No reports — the page renders the header + intel + evidence fallback (P7). */
export const findingDetailNoReport: FindingDetailResponse = base([])

/** Lookup by the fixture's finding id, mirroring the mock-serving pattern. */
const BY_SHAPE: Record<string, FindingDetailResponse> = {
  both: findingDetailBoth,
  'red-only': findingDetailRedOnly,
  'blue-only': findingDetailBlueOnly,
  'no-report': findingDetailNoReport,
}

/** Mock resolver (demo data) returning a shape by key, with simulated latency. */
export function findingDetailMock(shape: keyof typeof BY_SHAPE = 'both') {
  return withLatency(BY_SHAPE[shape] ?? findingDetailBoth)
}
