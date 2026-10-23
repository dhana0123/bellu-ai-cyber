// src/features/graph-explorer/components/GraphCanvas.tsx
//
// Graph canvas backed by Sigma.js v3/v4 + graphology (via @react-sigma/core).
// This replaces the previous hand-rolled pixi.js renderer + custom force
// simulation, LOD controller, label-culling, icon rasterization, and
// fit-to-view. Sigma handles all of that natively and far more robustly:
//   - WebGL rendering that scales to thousands of nodes
//   - image/icon nodes via @sigma/node-image's NodeImageProgram (each node
//     shows its real inventory icon — the exact bug we were fighting)
//   - GPU SDF labels that stay crisp and correctly sized at ANY zoom
//   - built-in pan / zoom / hover / click and camera animation (fit-to-view)
// Node positions come from a ForceAtlas2 layout (graphology-layout-forceatlas2).
//
// The external props are unchanged, so GraphExplorer.tsx needs no rewiring.

import { useEffect, useMemo, useState } from 'react'
import { Minus, Plus, Maximize2 } from 'lucide-react'
import Graph from 'graphology'
import forceAtlas2 from 'graphology-layout-forceatlas2'
import {
  SigmaContainer,
  useLoadGraph,
  useRegisterEvents,
  useSigma,
} from '@react-sigma/core'
import { NodeImageProgram } from '@sigma/node-image'
import '@react-sigma/core/lib/style.css'

import { Button } from '@/components/ui/button'
import { useTheme, type Theme } from '@/theme'
import type { GraphEdge, GraphNode } from '@/types/graphExplorer'
import { iconUrl, styleForKind } from '../lib/iconRegistry'

/**
 * Theme-aware colors for edges and labels so they read clearly on both themes:
 * light text/edges on the dark surface, dark text/edges on the light surface.
 */
interface GraphPalette {
  label: string // node/edge label text
  labelHalo: string // label outline/background for contrast
  edge: string // default edge stroke
  edgeDim: string // non-incident edge when a node is focused
  edgeIncident: string // edge incident to the SELECTED (clicked) node
  edgeHover: string // edge incident to the HOVERED node (softer)
  nodeDim: string // non-incident node disc when a node is focused
  hoverBg: string // hover label background box fill (a darker shade)
  hoverText: string // hover label text on the box
}

function paletteFor(theme: Theme): GraphPalette {
  return theme === 'dark'
    ? {
        label: '#e5e7eb',
        labelHalo: '#111827',
        edge: '#4b5563',
        edgeDim: '#2a2f3a',
        edgeIncident: '#94a3b8', // selection: clearer slate accent
        edgeHover: '#5b6472', // hover: very soft, low-contrast accent
        nodeDim: '#374151',
        hoverBg: '#0b0f17', // darker shade behind the hovered label
        hoverText: '#f1f5f9',
      }
    : {
        label: '#0f172a',
        labelHalo: '#ffffff',
        edge: '#cbd5e1',
        edgeDim: '#eef2f7',
        edgeIncident: '#94a3b8', // selection: clearer slate accent
        edgeHover: '#c7cdd6', // hover: very soft, low-contrast accent
        nodeDim: '#e2e8f0',
        hoverBg: '#1f2937', // dark box even in light theme, per request
        hoverText: '#f8fafc',
      }
}

/**
 * Custom hover renderer: draws the hovered node's label in a rounded, darker
 * background box (palette.hoverBg) with light text, so the name pops out on
 * hover. Mirrors Sigma's default disc-hover layout but with our shade.
 */
function makeDrawNodeHover(palette: GraphPalette) {
  return (
    context: CanvasRenderingContext2D,
    data: { x: number; y: number; size: number; label?: string | null },
    settings: { labelSize?: number; labelFont?: string; labelWeight?: string },
  ): void => {
    const label = data.label
    if (!label) return
    const size = settings.labelSize ?? 11
    const font = settings.labelFont ?? 'sans-serif'
    const weight = settings.labelWeight ?? 'normal'
    context.font = `${weight} ${size}px ${font}`

    const textW = context.measureText(label).width
    const padX = 8
    const padY = 5
    const boxW = textW + padX * 2
    const boxH = size + padY * 2
    const x = data.x + data.size + 4
    const y = data.y - boxH / 2

    // Rounded dark box.
    const r = 5
    context.beginPath()
    context.moveTo(x + r, y)
    context.lineTo(x + boxW - r, y)
    context.quadraticCurveTo(x + boxW, y, x + boxW, y + r)
    context.lineTo(x + boxW, y + boxH - r)
    context.quadraticCurveTo(x + boxW, y + boxH, x + boxW - r, y + boxH)
    context.lineTo(x + r, y + boxH)
    context.quadraticCurveTo(x, y + boxH, x, y + boxH - r)
    context.lineTo(x, y + r)
    context.quadraticCurveTo(x, y, x + r, y)
    context.closePath()
    context.fillStyle = palette.hoverBg
    context.fill()

    context.fillStyle = palette.hoverText
    context.textAlign = 'left'
    context.textBaseline = 'middle'
    context.fillText(label, x + padX, data.y)
  }
}

export interface GraphCanvasProps {
  nodes: GraphNode[]
  edges: GraphEdge[]
  selectedId: string | null
  onSelect: (id: string | null) => void
  /** Maps a node category to a 0xRRGGBB tint (kept for API compatibility). */
  colorForCategory: (category: string) => number
  /** Optional: switch the page to the accessible Table view (WebGL fallback). */
  onSwitchToTable?: () => void
}

// Node size scale (Sigma "screen" px at default zoom). Derived from degree so
// hubs read larger; clamped so leaves stay visible and hubs never dominate.
const MIN_SIZE = 10
const MAX_SIZE = 30

function webglAvailable(): boolean {
  try {
    const canvas = document.createElement('canvas')
    const gl =
      canvas.getContext('webgl2') ??
      canvas.getContext('webgl') ??
      canvas.getContext('experimental-webgl')
    return gl !== null && gl !== undefined
  } catch {
    return false
  }
}

/** `0xRRGGBB` number -> `#rrggbb` string for Sigma's color attribute. */
function hexToCss(color: number): string {
  return `#${(color & 0xffffff).toString(16).padStart(6, '0')}`
}

/**
 * Loads the working set into the Sigma graph: builds a graphology graph with
 * image nodes (real inventory icons), degree-based sizes, category colors, and
 * a ForceAtlas2 layout, then hands it to Sigma. Re-runs whenever the data
 * changes. Lives inside <SigmaContainer> so it can use the react-sigma hooks.
 */
function LoadGraph({
  nodes,
  edges,
  colorForCategory,
  palette,
}: {
  nodes: GraphNode[]
  edges: GraphEdge[]
  colorForCategory: (category: string) => number
  palette: GraphPalette
}): null {
  const loadGraph = useLoadGraph()
  const sigma = useSigma()

  useEffect(() => {
    const graph = new Graph({ multi: true, type: 'directed' })

    // Degree map for node sizing (incident edges with both endpoints present).
    const present = new Set(nodes.map((n) => n.id))
    const degree = new Map<string, number>()
    for (const n of nodes) degree.set(n.id, 0)
    for (const e of edges) {
      if (present.has(e.source) && present.has(e.target)) {
        degree.set(e.source, (degree.get(e.source) ?? 0) + 1)
        degree.set(e.target, (degree.get(e.target) ?? 0) + 1)
      }
    }
    let maxDeg = 0
    for (const d of degree.values()) if (d > maxDeg) maxDeg = d

    const sizeFor = (id: string): number => {
      if (maxDeg <= 0) return MIN_SIZE
      const t = Math.sqrt((degree.get(id) ?? 0) / maxDeg) // 0..1, sqrt-compressed
      return MIN_SIZE + (MAX_SIZE - MIN_SIZE) * t
    }

    // Seed initial positions on a circle; FA2 then spreads them out. Sigma
    // requires x/y on every node before layout.
    const n = Math.max(nodes.length, 1)
    nodes.forEach((node, i) => {
      if (graph.hasNode(node.id)) return
      const angle = (2 * Math.PI * i) / n
      graph.addNode(node.id, {
        x: Math.cos(angle) * 10,
        y: Math.sin(angle) * 10,
        size: sizeFor(node.id),
        label: node.name,
        color: hexToCss(colorForCategory(node.category) || styleForKind(node.kind).colorHex),
        // NodeImageProgram: the node's real inventory icon.
        type: 'image',
        image: iconUrl(node.provider, node.kind),
      })
    })

    for (const e of edges) {
      if (!graph.hasNode(e.source) || !graph.hasNode(e.target)) continue
      try {
        graph.addEdgeWithKey(e.id, e.source, e.target, {
          label: e.relation,
          size: 1,
          color: palette.edge,
          type: 'arrow',
        })
      } catch {
        // Duplicate key (parallel edge already added): ignore.
      }
    }

    // ForceAtlas2 layout: a few hundred synchronous iterations settle a graph
    // of this size quickly. barnesHut keeps it ~O(n log n) for large graphs.
    if (graph.order > 0) {
      const iterations = graph.order > 1500 ? 200 : 400
      forceAtlas2.assign(graph, {
        iterations,
        settings: {
          ...forceAtlas2.inferSettings(graph),
          barnesHutOptimize: graph.order > 1000,
          scalingRatio: 10,
          gravity: 1,
          adjustSizes: true,
        },
      })
    }

    loadGraph(graph)

    // Fit the whole graph in view once it is laid out.
    requestAnimationFrame(() => {
      try {
        sigma.getCamera().animatedReset({ duration: 300 })
      } catch {
        /* camera not ready */
      }
    })
  }, [nodes, edges, colorForCategory, palette, loadGraph, sigma])

  return null
}

/**
 * Wires Sigma interaction events to the page: click a node -> onSelect(id);
 * click empty space -> onSelect(null); and hover highlights the node + its
 * neighbors while dimming the rest. Also reflects the externally-selected node.
 */
function GraphEvents({
  selectedId,
  onSelect,
  palette,
}: {
  selectedId: string | null
  onSelect: (id: string | null) => void
  palette: GraphPalette
}): null {
  const sigma = useSigma()
  const registerEvents = useRegisterEvents()
  // Node currently under the pointer; a hover change re-runs the reducer
  // effect so the hovered node's label gets the highlight (background) box.
  const [hovered, setHovered] = useState<string | null>(null)

  useEffect(() => {
    registerEvents({
      clickNode: (e) => onSelect(e.node),
      clickStage: () => onSelect(null),
      enterNode: (e) => setHovered(e.node),
      leaveNode: () => setHovered(null),
    })
  }, [registerEvents, onSelect])

  // Highlight the "focused" node's connections. The focus is the selected node
  // if there is one, otherwise the hovered node. When a node is focused: it and
  // its direct neighbors stay bright (the neighbors' labels are forced on), the
  // connecting edges get the brighter incident color, and everything NOT in the
  // neighborhood fades to a dimmer shade — so hovering a node lights up exactly
  // what it connects to.
  useEffect(() => {
    const graph = sigma.getGraph()
    // Focus is the selected node if one is clicked, else the hovered node.
    const focus = selectedId ?? hovered
    // Hover-only focus (nothing selected) uses the SOFTER accent; a selection
    // uses the clearer accent.
    const hoverOnly = selectedId === null && hovered !== null
    const edgeAccent = hoverOnly ? palette.edgeHover : palette.edgeIncident
    const edgeAccentSize = hoverOnly ? 1 : 1.5

    sigma.setSetting('nodeReducer', (node, data) => {
      if (focus === null) return data
      if (node === focus) {
        return { ...data, zIndex: 2, highlighted: true, forceLabel: true }
      }
      if (graph.areNeighbors(focus, node)) {
        // Connected neighbor: keep it bright and reveal its name.
        return { ...data, zIndex: 1, forceLabel: true }
      }
      // Unrelated: dim it and drop its label so the neighborhood stands out.
      return { ...data, color: palette.nodeDim, label: '', zIndex: 0 }
    })

    sigma.setSetting('edgeReducer', (edge, data) => {
      if (focus === null) return data
      const src = graph.source(edge)
      const tgt = graph.target(edge)
      if (src === focus || tgt === focus) {
        // Edge connected to the focused node: softer on hover, clearer on select.
        return { ...data, color: edgeAccent, size: edgeAccentSize, zIndex: 1 }
      }
      return { ...data, color: palette.edgeDim, zIndex: 0 }
    })

    sigma.refresh()
  }, [sigma, selectedId, palette, hovered])

  return null
}

/** On-screen zoom controls bound to the Sigma camera. */
function ZoomControls(): JSX.Element {
  const sigma = useSigma()
  const zoom = (ratio: number): void => {
    const camera = sigma.getCamera()
    camera.animate({ ratio: camera.ratio * ratio }, { duration: 200 })
  }
  return (
    <div className="absolute right-3 top-3 z-10 flex flex-col gap-1 rounded-lg border border-border bg-background/80 p-1 shadow-sm backdrop-blur">
      <Button type="button" size="icon" variant="ghost" aria-label="Zoom in" onClick={() => zoom(1 / 1.3)}>
        <Plus className="h-4 w-4" aria-hidden="true" />
      </Button>
      <Button type="button" size="icon" variant="ghost" aria-label="Zoom out" onClick={() => zoom(1.3)}>
        <Minus className="h-4 w-4" aria-hidden="true" />
      </Button>
      <Button
        type="button"
        size="icon"
        variant="ghost"
        aria-label="Reset view"
        onClick={() => sigma.getCamera().animatedReset({ duration: 300 })}
      >
        <Maximize2 className="h-4 w-4" aria-hidden="true" />
      </Button>
    </div>
  )
}

export function GraphCanvas(props: GraphCanvasProps): JSX.Element {
  const { nodes, edges, selectedId, onSelect, colorForCategory, onSwitchToTable } = props

  // WebGL fallback: Sigma needs WebGL; if unavailable, offer the Table view.
  const hasWebgl = useMemo(() => webglAvailable(), [])

  // Theme-aware colors so labels + edges read clearly on dark and light.
  const { theme } = useTheme()
  const palette = useMemo(() => paletteFor(theme), [theme])

  const settings = useMemo(
    () => ({
      nodeProgramClasses: { image: NodeImageProgram },
      defaultNodeType: 'image',
      defaultEdgeType: 'arrow',
      renderLabels: true,
      renderEdgeLabels: true,
      // Label de-cluttering: Sigma only draws labels that don't collide, using
      // a screen-space grid. A LOW density + a LARGER grid cell means fewer
      // labels show at once (only non-overlapping ones), and more are revealed
      // progressively as you zoom in. Small font keeps them tidy.
      labelDensity: 0.07,
      labelGridCellSize: 150,
      labelRenderedSizeThreshold: 12,
      labelSize: 11,
      edgeLabelSize: 10,
      zIndex: true,
      // Theme-aware label colors so names stay legible on either theme.
      labelColor: { color: palette.label },
      edgeLabelColor: { color: palette.label },
      labelFont: 'ui-sans-serif, system-ui, sans-serif',
      edgeLabelFont: 'ui-sans-serif, system-ui, sans-serif',
      // On hover, draw the node's label in a darker rounded box (theme-aware).
      defaultDrawNodeHover: makeDrawNodeHover(palette),
    }),
    [palette],
  )

  if (!hasWebgl) {
    return (
      <div
        role="region"
        aria-label="Graph canvas unavailable"
        className="flex h-full min-h-0 flex-1 flex-col items-center justify-center gap-3 p-6 text-center"
      >
        <p className="max-w-md text-sm text-muted-foreground">
          The interactive graph needs WebGL, which isn&apos;t available in this
          browser or environment. All graph data is fully accessible in the Table
          view.
        </p>
        {onSwitchToTable !== undefined ? (
          <Button variant="outline" size="sm" onClick={onSwitchToTable}>
            Switch to Table view
          </Button>
        ) : (
          <p className="text-xs text-muted-foreground">
            Use the Graph / Table toggle above to switch to Table view.
          </p>
        )}
      </div>
    )
  }

  return (
    <div
      role="img"
      aria-label="Interactive security graph. Pan by dragging, zoom with the scroll wheel or the zoom controls, and click a node to select it. The Table view offers the same data for keyboard and screen-reader users."
      className="relative h-full min-h-0 w-full flex-1 overflow-hidden bg-card"
    >
      <SigmaContainer
        settings={settings}
        style={{ height: '100%', width: '100%', background: 'hsl(var(--card))' }}
        className="bg-card"
      >
        <LoadGraph
          nodes={nodes}
          edges={edges}
          colorForCategory={colorForCategory}
          palette={palette}
        />
        <GraphEvents selectedId={selectedId} onSelect={onSelect} palette={palette} />
        <ZoomControls />
      </SigmaContainer>
    </div>
  )
}

export default GraphCanvas
