// src/features/graph-explorer/lib/iconRegistry.ts
//
// Single source of truth mapping an entity `kind` to a Lucide icon + accent
// color, and a `provider` key to a bundled brand SVG. Consumed by the pixi
// canvas (as `Texture`s) and re-exported for the inventory `KindIcon` /
// `ProviderIcon` and the trace-graph `GraphNode` so all three stay visually
// identical (Requirement #3: one map, not three).
//
// The canvas rasterizes the *exact* Lucide React glyph the DOM renders (via
// `renderToStaticMarkup`) so the on-canvas mark matches the inventory icon
// rather than a hand-rolled SVG path. Provider nodes use the same brand SVGs
// the inventory table uses (src/assets/providers/*.svg).

import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import {
  Globe,
  Bot,
  BrainCircuit,
  Boxes,
  KeyRound,
  Database,
  Cloud,
  Server,
  Bug,
  Cpu,
  Network,
  Box,
  Container,
  GitBranch,
  Package,
  Workflow,
  MessageSquareCode,
  PlugZap,
  Spline,
  DoorOpen,
  type LucideIcon,
} from 'lucide-react'

import awsLogo from '@/assets/providers/aws.svg'
import azureLogo from '@/assets/providers/azure.svg'
import gcpLogo from '@/assets/providers/gcp.svg'
import k8sLogo from '@/assets/providers/k8s.svg'
import dockerLogo from '@/assets/providers/docker.svg'
import githubLogo from '@/assets/providers/github.svg'
import gitlabLogo from '@/assets/providers/gitlab.svg'
import bitbucketLogo from '@/assets/providers/bitbucket.svg'

/** Accent color + icon for one canonical entity kind. Hex is 0xRRGGBB. */
export interface KindStyle {
  /** Lucide icon component, rendered both in the DOM and rasterized to canvas. */
  icon: LucideIcon
  /** 0xRRGGBB, for pixi tint. */
  colorHex: number
  /** '#rrggbb', matches colorHex, for React/style consumers and SVG strokes. */
  colorCss: string
  /** Human sub-label, e.g. 'Data Store'. */
  label: string
}

/**
 * The authoritative kind -> style map. Keys are the UNION of the two previous
 * maps so neither consumer regresses:
 *   - features/ai-inventory/components/category.tsx `KIND_META`
 *   - components/graph/GraphNode.tsx `KIND_STYLE`
 *
 * Where the two sources disagreed on icon/color we prefer the concrete hex
 * colors from GraphNode.tsx; kinds present only in `KIND_META` take the
 * Tailwind-500 hex of that map's text color.
 */
export const KIND_STYLE: Readonly<Record<string, KindStyle>> = {
  // Each canonical entity kind gets a DISTINCT icon + accent color so the type
  // is recognizable at a glance, both in the inventory table's first column
  // (via KindIcon) and as the graph node image (via iconUrl). Icons are from
  // lucide-react (the project's installed React icon set).
  CloudAccount: { icon: Cloud, colorHex: 0x64748b, colorCss: '#64748b', label: 'Cloud Account' },
  Identity: { icon: KeyRound, colorHex: 0xf59e0b, colorCss: '#f59e0b', label: 'Identity' },
  DataStore: { icon: Database, colorHex: 0x10b981, colorCss: '#10b981', label: 'Data Store' },
  Compute: { icon: Server, colorHex: 0x3b82f6, colorCss: '#3b82f6', label: 'Compute' },
  Cluster: { icon: Boxes, colorHex: 0x06b6d4, colorCss: '#06b6d4', label: 'Cluster' },
  Workload: { icon: Workflow, colorHex: 0x8b5cf6, colorCss: '#8b5cf6', label: 'Workload' },
  ContainerImage: {
    icon: Container,
    colorHex: 0x0ea5e9,
    colorCss: '#0ea5e9',
    label: 'Container Image',
  },
  Repository: { icon: GitBranch, colorHex: 0xd946ef, colorCss: '#d946ef', label: 'Repository' },
  Package: { icon: Package, colorHex: 0xf97316, colorCss: '#f97316', label: 'Package' },
  Vulnerability: { icon: Bug, colorHex: 0xef4444, colorCss: '#ef4444', label: 'Vulnerability' },
  AIAgent: { icon: Bot, colorHex: 0x22c55e, colorCss: '#22c55e', label: 'AI Agent' },
  AIModel: { icon: BrainCircuit, colorHex: 0x6366f1, colorCss: '#6366f1', label: 'AI Model' },
  Prompt: {
    icon: MessageSquareCode,
    colorHex: 0xeab308,
    colorCss: '#eab308',
    label: 'Prompt',
  },
  MCPServer: { icon: PlugZap, colorHex: 0x14b8a6, colorCss: '#14b8a6', label: 'MCP Server' },
  AIService: { icon: Cpu, colorHex: 0xec4899, colorCss: '#ec4899', label: 'AI Service' },
  NetworkEndpoint: {
    icon: Spline,
    colorHex: 0xf43f5e,
    colorCss: '#f43f5e',
    label: 'Network Endpoint',
  },
  ExternalService: {
    icon: Globe,
    colorHex: 0x71717a,
    colorCss: '#71717a',
    label: 'External Service',
  },
  Network: { icon: Network, colorHex: 0x0891b2, colorCss: '#0891b2', label: 'Network' },
  EntryPoint: { icon: DoorOpen, colorHex: 0xa855f7, colorCss: '#a855f7', label: 'Entry Point' },
  // Non-canonical but used by the trace graph.
  Internet: { icon: Globe, colorHex: 0x3b82f6, colorCss: '#3b82f6', label: 'Internet' },
}

/** Neutral fallback for unknown kinds (box glyph + muted color). */
export const KIND_FALLBACK: KindStyle = {
  icon: Box,
  colorHex: 0x9ca3af,
  colorCss: '#9ca3af',
  label: 'Resource',
}

/** Resolve a kind to its style, never throwing (falls back on unknown). */
export function styleForKind(kind: string): KindStyle {
  return KIND_STYLE[kind] ?? KIND_FALLBACK
}

// --- Cloud / VCS provider brand marks ---------------------------------------

/**
 * provider key (lowercased) -> bundled brand SVG asset URL. Mirrors
 * PROVIDER_LOGO in features/ai-inventory/components/category.tsx so graph nodes
 * match the rest of the UI.
 */
export const PROVIDER_LOGO: Readonly<Record<string, string>> = {
  aws: awsLogo,
  gcp: gcpLogo,
  google: gcpLogo,
  googlecloud: gcpLogo,
  azure: azureLogo,
  k8s: k8sLogo,
  kubernetes: k8sLogo,
  image: dockerLogo,
  docker: dockerLogo,
  // CI / VCS brand marks.
  github: githubLogo,
  gitlab: gitlabLogo,
  atlassian: bitbucketLogo,
  bitbucket: bitbucketLogo,
}

/** provider key (lowercased) -> bundled brand SVG asset URL, or undefined. */
export function providerLogoUrl(provider: string | undefined): string | undefined {
  return PROVIDER_LOGO[(provider ?? '').toLowerCase()]
}

// --- Icon URLs for Sigma's NodeImageProgram ---------------------------------
//
// Sigma renders image nodes from a URL (asset path or data URI), not a pixi
// texture. We ALWAYS render the kind's Lucide glyph (the exact icon the
// inventory table shows in its first column via KindIcon) to an inline SVG data
// URI on a soft rounded white tile, so a node's type is recognizable at a
// glance and the graph matches the inventory row-for-row. The provider is
// intentionally ignored here (provider brand marks still appear elsewhere in
// the UI via ProviderIcon). Results are cached by (kind,color).

const ICON_PX = 128

const urlCache = new Map<string, string>()

// Fraction of the tile the glyph occupies; the remainder is padding so the
// white icon reads clearly inside the solid colored tile.
const GLYPH_SCALE = 0.56

/**
 * A SOLID colored rounded tile (the kind's accent color) with a WHITE Lucide
 * glyph centered on top, padded so it reads clearly, returned as an SVG data
 * URI. The glyph (native 24x24) is scaled to GLYPH_SCALE of the tile and
 * centered via a transform so equal padding surrounds it on all sides.
 */
function kindGlyphDataUri(style: KindStyle): string {
  // White icon markup on a transparent background; the tile provides the color.
  const inner = renderToStaticMarkup(
    createElement(style.icon, {
      color: '#ffffff',
      width: 24,
      height: 24,
      strokeWidth: 2,
    }),
  )
  // Tile is a 24x24 box. Scale the 24x24 glyph down to GLYPH_SCALE and translate
  // so the scaled glyph is centered (equal padding on every side).
  const g = GLYPH_SCALE
  const offset = (24 - 24 * g) / 2
  const svg =
    `<svg xmlns="http://www.w3.org/2000/svg" width="${ICON_PX}" height="${ICON_PX}" viewBox="0 0 24 24">` +
    `<rect x="0" y="0" width="24" height="24" rx="6" fill="${style.colorCss}"/>` +
    `<g transform="translate(${offset} ${offset}) scale(${g})">${inner}</g>` +
    `</svg>`
  return `data:image/svg+xml;utf8,${encodeURIComponent(svg)}`
}

/**
 * Returns an inline SVG data URI of the KIND's Lucide glyph (tinted with the
 * kind color) suitable for Sigma's NodeImageProgram `image` attribute. The icon
 * is chosen purely by entity kind so every node shows the same mark the
 * inventory table's first column shows for that type, regardless of provider.
 * The `provider` parameter is kept for call-site compatibility but unused.
 * Cached per (kind,color).
 */
export function iconUrl(_provider: string | undefined, kind: string): string {
  const style = styleForKind(kind)
  const cacheKey = `k:${kind}:${style.colorHex}`

  const hit = urlCache.get(cacheKey)
  if (hit !== undefined) return hit

  const url = kindGlyphDataUri(style)
  urlCache.set(cacheKey, url)
  return url
}
