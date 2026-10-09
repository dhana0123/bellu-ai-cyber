import { describe, expect, it } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { afterEach } from 'vitest'
import Gateway from '@/features/gateway/Gateway'
import Runtime from '@/features/runtime/Runtime'
import Agents from '@/features/agents/Agents'
import DataSecurity from '@/features/data/DataSecurity'
import SupplyChain from '@/features/supply-chain/SupplyChain'
import Media from '@/features/media/Media'
import Governance from '@/features/governance/Governance'

const MODULES: [string, () => JSX.Element][] = [
  ['AI Gateway', Gateway],
  ['Runtime Defense', Runtime],
  ['Agent Security', Agents],
  ['AI Data Security', DataSecurity],
  ['AI Supply Chain', SupplyChain],
  ['Media Authenticity', Media],
  ['Governance', Governance],
]

afterEach(() => cleanup())

describe('add-on modules — Preview labeling', () => {
  it.each(MODULES)('%s renders the Preview — demo data badge', (_title, Comp) => {
    render(<Comp />)
    // The persistent PreviewBadge appears in each add-on header.
    expect(screen.getByText(/Preview — demo data/i)).toBeInTheDocument()
  })

  it.each(MODULES)('%s never claims real/live telemetry', (_title, Comp) => {
    const { container } = render(<Comp />)
    const text = container.textContent ?? ''
    expect(text).not.toMatch(/real telemetry/i)
    expect(text).not.toMatch(/live telemetry/i)
    expect(text).not.toMatch(/real customer data/i)
  })
})
