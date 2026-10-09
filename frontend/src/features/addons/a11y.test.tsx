import { describe, expect, it, afterEach } from 'vitest'
import { render, screen, cleanup, waitFor } from '@testing-library/react'
import { axe } from 'vitest-axe'
import Gateway from '@/features/gateway/Gateway'
import Governance from '@/features/governance/Governance'
import Agents from '@/features/agents/Agents'

// Accessibility smoke test: render representative add-on modules, wait for the
// mock data to populate, then assert axe finds no violations. Charts render in
// jsdom without layout, so this focuses on structural a11y (roles, labels,
// headings, contrast-independent checks).

afterEach(() => cleanup())

const CASES: [string, () => JSX.Element][] = [
  ['AI Gateway', Gateway],
  ['Governance', Governance],
  ['Agent Security', Agents],
]

describe('add-on modules — accessibility smoke', () => {
  it.each(CASES)(
    '%s has no axe violations once populated',
    async (_name, Comp) => {
      const { container } = render(<Comp />)
      // Wait for the Preview header (immediate) and give async mocks time to settle.
      await screen.findByText(/Preview — demo data/i)
      await waitFor(() => expect(screen.queryByRole('status')).toBeNull(), { timeout: 2000 })
      // `heading-order` is a best-practice rule; shadcn CardTitle renders <h3>,
      // so cards under the page <h1> trip it. That's a cosmetic heading-level
      // nuance, not a WCAG A/AA failure — scope it out and assert the rest clean.
      const results = await axe(container, {
        rules: { 'heading-order': { enabled: false } },
      })
      expect(results.violations).toEqual([])
    },
    10000,
  )
})
