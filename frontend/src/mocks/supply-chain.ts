import { withLatency } from './_helpers'
import type { ModelRegistryEntry, ProvenanceRecord, ScanResult } from '@/types/addons'

const DEMO_MODELS: ModelRegistryEntry[] = [
  {
    id: 'm-1',
    name: 'recommender-v3',
    framework: 'PyTorch 2.3',
    hash: 'sha256:9f2a…c71',
    source: 'internal',
    criticality: 'high',
    signed: true,
  },
  {
    id: 'm-2',
    name: 'llama-3-8b-ft',
    framework: 'Transformers',
    hash: 'sha256:1be0…4ad',
    source: 'huggingface',
    criticality: 'medium',
    signed: false,
  },
  {
    id: 'm-3',
    name: 'fraud-scorer',
    framework: 'XGBoost',
    hash: 'sha256:77cd…e02',
    source: 'internal',
    criticality: 'critical',
    signed: true,
  },
]

const DEMO_SCANS: ScanResult[] = [
  {
    id: 's-1',
    artifact: 'llama-3-8b-ft',
    component: 'pkg:pypi/transformers',
    cve: 'CVE-2025-00123',
    severity: 'high',
    fixed_version: '4.46.1',
  },
  {
    id: 's-2',
    artifact: 'recommender-v3',
    component: 'pkg:pypi/torch',
    cve: 'CVE-2025-04567',
    severity: 'medium',
    fixed_version: '2.3.1',
  },
  {
    id: 's-3',
    artifact: 'fraud-scorer',
    component: 'pkg:pypi/numpy',
    cve: 'CVE-2024-09911',
    severity: 'low',
  },
]

const DEMO_PROVENANCE: ProvenanceRecord[] = [
  {
    id: 'p-1',
    model: 'recommender-v3',
    built_by: 'ci-pipeline',
    built_at: '2026-09-20T12:00:00Z',
    dataset_ref: 's3://datasets/reco-2026Q3',
    signature_verified: true,
  },
  {
    id: 'p-2',
    model: 'llama-3-8b-ft',
    built_by: 'ml-notebook',
    built_at: '2026-09-28T18:30:00Z',
    dataset_ref: 'hf://datasets/support-chats',
    signature_verified: false,
  },
]

export const supplyChainMock = {
  models: () => withLatency(DEMO_MODELS),
  scans: () => withLatency(DEMO_SCANS),
  provenance: () => withLatency(DEMO_PROVENANCE),
}
