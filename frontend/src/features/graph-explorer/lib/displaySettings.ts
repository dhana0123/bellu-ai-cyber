// Display toggles for the graph canvas, exposed through the on-canvas settings
// menu. These control how Sigma renders (node/edge labels, directed arrows)
// without touching the underlying data.

export interface GraphDisplaySettings {
  nodeLabels: boolean
  edgeLabels: boolean
  arrows: boolean
}

export const DEFAULT_DISPLAY_SETTINGS: GraphDisplaySettings = {
  nodeLabels: true,
  edgeLabels: false,
  arrows: true,
}
