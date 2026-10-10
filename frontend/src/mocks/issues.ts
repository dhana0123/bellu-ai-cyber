import { withLatency } from './_helpers'
import type { IssuesData } from '@/types/issues'

// DEMO data for the Risk Issues page. Values mirror the UI spec; never real
// customer telemetry. Served through withLatency like the other mock modules.

const COMMON_RISKS = [
  { id: 'r1', label: 'Internet exposed' },
  { id: 'r2', label: 'Remote code execution' },
  { id: 'r3', label: 'Known exploited' },
]

const DEMO_ISSUES: IssuesData = {
  severity_metrics: [
    { severity: 'critical', letter: 'C', count: 8, label: 'Critical Issues', trend_pct: 700 },
    { severity: 'high', letter: 'H', count: 0, label: 'High Issues', trend_pct: 0 },
    { severity: 'medium', letter: 'M', count: 0, label: 'Medium Issues', trend_pct: 0 },
    { severity: 'low', letter: 'L', count: 0, label: 'Low Issues', trend_pct: 0 },
  ],

  open_resolved: [
    { x: 'Dec 14', opened: 2, resolved: 1, new_resource: 2, opened_change: 2, resolved_change: 1 },
    { x: 'Dec 15', opened: 10, resolved: 2, new_resource: 8, opened_change: 8, resolved_change: 0 },
    { x: 'Dec 16', opened: 11, resolved: 4, new_resource: 1, opened_change: 1, resolved_change: 2 },
    { x: 'Dec 17', opened: 11, resolved: 5, new_resource: 0, opened_change: 0, resolved_change: 1 },
    { x: 'Dec 18', opened: 12, resolved: 7, new_resource: 1, opened_change: 1, resolved_change: 2 },
    { x: 'Dec 19', opened: 12, resolved: 8, new_resource: 0, opened_change: 0, resolved_change: 1 },
    { x: 'Dec 20', opened: 12, resolved: 10, new_resource: 0, opened_change: 0, resolved_change: 2 },
  ],

  open_issues_trend: [
    { x: 'Dec 10', critical: 0, high: 4 },
    { x: 'Dec 12', critical: 0, high: 4 },
    { x: 'Dec 14', critical: 1, high: 4 },
    { x: 'Dec 15', critical: 8, high: 4 },
    { x: 'Dec 16', critical: 10, high: 4 },
    { x: 'Dec 18', critical: 12, high: 4 },
    { x: 'Dec 20', critical: 12, high: 4 },
  ],
  open_issues_trend_pct: 700,

  issues: [
    {
      id: 'iss-1',
      title:
        'Externally validated Next.js insecure deserialization vulnerability leading to RCE',
      severity: 'critical',
      status: 'open',
      resource: 'http://3.129.152.166:80',
      internet_facing: true,
      ticket: null,
      risks: COMMON_RISKS,
      project_count: 5,
      subscription: 'wizwashers-prod-68c252-connector',
      created: 'Dec 16, 2025, 12:51 AM',
    },
    {
      id: 'iss-2',
      title:
        'Externally validated software configured with default or weak credentials',
      severity: 'critical',
      status: 'in_progress',
      resource: 'http://us-east-2.compute.amazonaws.com',
      internet_facing: true,
      ticket: null,
      risks: [COMMON_RISKS[0], COMMON_RISKS[2]],
      project_count: 3,
      subscription: 'wizwashers-prod-68c252-connector',
      created: 'Dec 16, 2025, 01:14 AM',
    },
    {
      id: 'iss-3',
      title:
        'Internet-facing container using an image with an initial-access vulnerability',
      severity: 'critical',
      status: 'open',
      resource: 'http://18.223.14.9:8080',
      internet_facing: true,
      ticket: 'JIRA-4821',
      risks: COMMON_RISKS,
      project_count: 2,
      subscription: 'wizwashers-stage-41a0-connector',
      created: 'Dec 16, 2025, 02:03 AM',
    },
    {
      id: 'iss-4',
      title: 'Publicly accessible storage bucket containing sensitive data',
      severity: 'critical',
      status: 'open',
      resource: 's3://wizwashers-prod-assets',
      internet_facing: true,
      ticket: null,
      risks: [COMMON_RISKS[0]],
      project_count: 1,
      subscription: 'wizwashers-prod-68c252-connector',
      created: 'Dec 15, 2025, 09:22 PM',
    },
    {
      id: 'iss-5',
      title: 'Over-privileged IAM role attached to an internet-facing workload',
      severity: 'critical',
      status: 'in_progress',
      resource: 'arn:aws:iam::001018342000:role/web-app',
      internet_facing: false,
      ticket: 'JIRA-4830',
      risks: [COMMON_RISKS[1]],
      project_count: 4,
      subscription: 'wizwashers-prod-68c252-connector',
      created: 'Dec 15, 2025, 06:40 PM',
    },
    {
      id: 'iss-6',
      title: 'Unpatched OS package with a known-exploited CVE',
      severity: 'critical',
      status: 'open',
      resource: 'http://52.14.120.3:443',
      internet_facing: true,
      ticket: null,
      risks: [COMMON_RISKS[2]],
      project_count: 6,
      subscription: 'wizwashers-stage-41a0-connector',
      created: 'Dec 15, 2025, 03:11 PM',
    },
    {
      id: 'iss-7',
      title: 'Secrets committed to a public code repository',
      severity: 'critical',
      status: 'resolved',
      resource: 'github.com/wizwashers/web-app',
      internet_facing: true,
      ticket: 'JIRA-4799',
      risks: [COMMON_RISKS[0], COMMON_RISKS[2]],
      project_count: 1,
      subscription: 'wizwashers-prod-68c252-connector',
      created: 'Dec 14, 2025, 11:05 AM',
    },
    {
      id: 'iss-8',
      title: 'Exposed management port on an internet-facing instance',
      severity: 'critical',
      status: 'open',
      resource: 'http://3.19.44.77:22',
      internet_facing: true,
      ticket: null,
      risks: COMMON_RISKS,
      project_count: 2,
      subscription: 'wizwashers-stage-41a0-connector',
      created: 'Dec 14, 2025, 08:48 AM',
    },
  ],
}

export const issuesMock = {
  overview: () => withLatency(DEMO_ISSUES),
}
