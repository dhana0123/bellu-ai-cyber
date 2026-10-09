import { describe, expect, it } from 'vitest'
import { client } from './client'
import { ADDON_PERMISSIONS } from '@/types/addons'

// These tests exercise the mock seam only (no network). They assert that the
// client exposes the foundation surface and that each add-on namespace returns
// typed demo data of the expected shape.

describe('client seam', () => {
  it('exposes foundation methods (real backend surface)', () => {
    expect(typeof client.login).toBe('function')
    expect(typeof client.findings).toBe('function')
    expect(typeof client.connectors).toBe('function')
  })

  it('exposes all seven add-on namespaces', () => {
    expect(client.gateway).toBeDefined()
    expect(client.runtime).toBeDefined()
    expect(client.agents).toBeDefined()
    expect(client.data).toBeDefined()
    expect(client.supplyChain).toBeDefined()
    expect(client.media).toBeDefined()
    expect(client.governance).toBeDefined()
  })

  it('declares one preview permission per add-on module', () => {
    expect(ADDON_PERMISSIONS).toHaveLength(7)
  })
})

describe('gateway mock', () => {
  it('returns typed stats', async () => {
    const stats = await client.gateway.stats()
    expect(stats.total_calls_24h).toBeGreaterThan(0)
    expect(typeof stats.avg_latency_ms).toBe('number')
  })

  it('returns routing policies with required fields', async () => {
    const policies = await client.gateway.policies()
    expect(policies.length).toBeGreaterThan(0)
    for (const p of policies) {
      expect(p).toHaveProperty('id')
      expect(p).toHaveProperty('destination')
      expect(typeof p.enabled).toBe('boolean')
    }
  })
})

describe('media mock', () => {
  it('returns a queue and resolves a single item by id', async () => {
    const queue = await client.media.queue()
    expect(queue.length).toBeGreaterThan(0)
    const first = queue[0]
    const item = await client.media.item(first.id)
    expect(item?.id).toBe(first.id)
    expect(item?.authenticity_score).toBeGreaterThanOrEqual(0)
    expect(item?.authenticity_score).toBeLessThanOrEqual(100)
  })

  it('returns null for an unknown id', async () => {
    const item = await client.media.item('does-not-exist')
    expect(item).toBeNull()
  })
})

describe('governance mock', () => {
  it('returns frameworks where passing <= total', async () => {
    const frameworks = await client.governance.frameworks()
    for (const f of frameworks) {
      expect(f.controls_passing).toBeLessThanOrEqual(f.controls_total)
    }
  })
})
