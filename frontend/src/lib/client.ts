// The single data seam for the whole app.
//
// Foundation modules (AI-SPM, connectors, admin) call the REAL backend through
// `realApi`. The seven add-on modules have no backend yet, so they call the
// typed mock layer in `src/mocks/*`.
//
// SWAP PATH (frontend-only today → real backend later): when an add-on endpoint
// ships, replace that one mock reference with the typed real call, e.g.
//     gateway: gatewayMock            →   gateway: realApi.gatewayXYZ
// Nothing else in the app changes — pages import from `client`, not from mocks.
import { realApi } from './api'
import { gatewayMock } from '@/mocks/gateway'
import { runtimeMock } from '@/mocks/runtime'
import { agentsMock } from '@/mocks/agents'
import { dataMock } from '@/mocks/data'
import { supplyChainMock } from '@/mocks/supply-chain'
import { mediaMock } from '@/mocks/media'
import { governanceMock } from '@/mocks/governance'
import { dashboardMock } from '@/mocks/dashboard'
import { issuesMock } from '@/mocks/issues'

export const client = {
  // --- Foundation (REAL backend) ---
  ...realApi,

  // --- Add-ons (MOCK — demo data only, see src/mocks/*) ---
  gateway: gatewayMock,
  runtime: runtimeMock,
  agents: agentsMock,
  data: dataMock,
  supplyChain: supplyChainMock,
  media: mediaMock,
  governance: governanceMock,
  dashboard: dashboardMock,
  issues: issuesMock,
} as const

export type Client = typeof client
