import { withLatency } from './_helpers'
import type { DataAsset, DlpPolicy, LineageFlow } from '@/types/addons'

const DEMO_DATA_ASSETS: DataAsset[] = [
  {
    id: 'da-1',
    name: 'customers.pii',
    data_type: 'PII',
    location: 'postgres://prod/customers',
    owning_team: 'platform',
    sensitivity: 'high',
  },
  {
    id: 'da-2',
    name: 'billing.cards',
    data_type: 'financial',
    location: 's3://billing-vault',
    owning_team: 'finance',
    sensitivity: 'critical',
  },
  {
    id: 'da-3',
    name: 'model-weights.ip',
    data_type: 'IP',
    location: 's3://ml-artifacts',
    owning_team: 'ml',
    sensitivity: 'high',
  },
]

const DEMO_LINEAGE: LineageFlow[] = [
  {
    id: 'lf-1',
    source: 'customers.pii',
    via: 'support-bot',
    destination: 'openai:gpt-4o',
    policy_violation: true,
  },
  {
    id: 'lf-2',
    source: 'docs.kb',
    via: 'rag-index',
    destination: 'anthropic:claude-3',
    policy_violation: false,
  },
]

const DEMO_DLP: DlpPolicy[] = [
  {
    id: 'dlp-1',
    name: 'Block SSNs in prompts',
    pattern: '\\d{3}-\\d{2}-\\d{4}',
    category: 'PII',
    action: 'block',
    matches_24h: 9,
    enabled: true,
  },
  {
    id: 'dlp-2',
    name: 'Warn on card numbers',
    pattern: '(?:\\d[ -]*?){13,16}',
    category: 'financial',
    action: 'warn',
    matches_24h: 4,
    enabled: true,
  },
]

export const dataMock = {
  assets: () => withLatency(DEMO_DATA_ASSETS),
  lineage: () => withLatency(DEMO_LINEAGE),
  dlp: () => withLatency(DEMO_DLP),
}
