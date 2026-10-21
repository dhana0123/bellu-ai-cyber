import { describe, expect, it } from 'vitest'
import {
  KIND_FALLBACK,
  KIND_STYLE,
  iconUrl,
  providerLogoUrl,
  styleForKind,
} from './iconRegistry'

// Unit tests for the shared icon/color registry.
//
//   Icon/color parity: the registry is the single source of truth, so each
//   KIND_STYLE entry is internally consistent (colorCss === '#' + colorHex) and
//   styleForKind resolves to that exact entry (or KIND_FALLBACK on unknown).
//   providerLogoUrl resolves a string for every known provider and undefined
//   for unknown ones — the parity surface the inventory consumers read from.
//
//   iconUrl identity: identical (provider|kind) inputs return the SAME cached
//   URL string, and a provider-backed node resolves the provider brand URL
//   while a kind-only node resolves a distinct kind-glyph data URI.

describe('icon/color parity', () => {
  it('every KIND_STYLE entry encodes the same color as hex and css', () => {
    for (const [kind, style] of Object.entries(KIND_STYLE)) {
      const expectedCss = '#' + style.colorHex.toString(16).padStart(6, '0')
      expect(style.colorCss.toLowerCase(), `colorCss parity for ${kind}`).toBe(expectedCss)
    }
  })

  it('every KIND_STYLE entry exposes a non-empty label and an icon component', () => {
    for (const [kind, style] of Object.entries(KIND_STYLE)) {
      expect(style.icon, `icon present for ${kind}`).toBeTruthy()
      expect(typeof style.label, `label is a string for ${kind}`).toBe('string')
      expect(style.label.length, `label non-empty for ${kind}`).toBeGreaterThan(0)
    }
  })

  it('styleForKind returns the exact KIND_STYLE entry for every known kind', () => {
    for (const kind of Object.keys(KIND_STYLE)) {
      expect(styleForKind(kind)).toBe(KIND_STYLE[kind])
    }
  })

  it('styleForKind returns KIND_FALLBACK for unknown kinds', () => {
    for (const unknown of ['', 'NotARealKind', 'datastore', 'resource']) {
      expect(styleForKind(unknown)).toBe(KIND_FALLBACK)
    }
  })

  it('KIND_FALLBACK is itself internally color-consistent', () => {
    const expectedCss = '#' + KIND_FALLBACK.colorHex.toString(16).padStart(6, '0')
    expect(KIND_FALLBACK.colorCss.toLowerCase()).toBe(expectedCss)
    expect(KIND_FALLBACK.icon).toBeTruthy()
    expect(KIND_FALLBACK.label.length).toBeGreaterThan(0)
  })

  it('providerLogoUrl resolves a string for every known provider', () => {
    const known = ['aws', 'github', 'gcp', 'azure', 'k8s', 'docker', 'gitlab', 'bitbucket']
    for (const provider of known) {
      const url = providerLogoUrl(provider)
      expect(typeof url, `logo url for ${provider}`).toBe('string')
      expect((url as string).length, `logo url non-empty for ${provider}`).toBeGreaterThan(0)
    }
  })

  it('providerLogoUrl is case-insensitive on the provider key', () => {
    expect(providerLogoUrl('AWS')).toBe(providerLogoUrl('aws'))
    expect(providerLogoUrl('GitHub')).toBe(providerLogoUrl('github'))
  })

  it('providerLogoUrl returns undefined for unknown or empty providers', () => {
    for (const unknown of ['', 'heroku', 'not-a-provider', 'AWSS']) {
      expect(providerLogoUrl(unknown)).toBeUndefined()
    }
    expect(providerLogoUrl(undefined)).toBeUndefined()
  })
})

describe('iconUrl (Sigma node image source)', () => {
  it('returns the same cached URL string for identical inputs', () => {
    const a = iconUrl('aws', 'Compute')
    const b = iconUrl('aws', 'Compute')
    expect(typeof a).toBe('string')
    expect(b).toBe(a)
  })

  it('always returns the KIND glyph data URI, ignoring the provider', () => {
    // Same kind + different providers must resolve to the same kind glyph, and
    // it is always an inline SVG data URI (never a provider brand asset URL).
    const awsCompute = iconUrl('aws', 'Compute')
    const ghCompute = iconUrl('github', 'Compute')
    const noProvCompute = iconUrl(undefined, 'Compute')
    expect(awsCompute.startsWith('data:image/svg+xml')).toBe(true)
    expect(ghCompute).toBe(awsCompute)
    expect(noProvCompute).toBe(awsCompute)
    // And it is NOT the provider brand URL.
    expect(awsCompute).not.toBe(providerLogoUrl('aws'))
  })

  it('distinct kinds produce distinct URLs (one icon per type)', () => {
    const dataStore = iconUrl('aws', 'DataStore')
    const vulnerability = iconUrl('aws', 'Vulnerability')
    const compute = iconUrl('aws', 'Compute')
    expect(vulnerability).not.toBe(dataStore)
    expect(compute).not.toBe(dataStore)
    expect(compute).not.toBe(vulnerability)
  })
})
