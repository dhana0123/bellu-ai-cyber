// Display toggles for the graph canvas, exposed through the on-canvas settings
// menu. These control how Sigma renders (node/edge labels, directed arrows)
// without touching the underlying data. The user's choice is persisted to
// localStorage so it survives reloads.

export interface GraphDisplaySettings {
  nodeLabels: boolean
  edgeLabels: boolean
  arrows: boolean
}

// Labels default OFF (cleaner canvas on large graphs); arrows stay on.
export const DEFAULT_DISPLAY_SETTINGS: GraphDisplaySettings = {
  nodeLabels: false,
  edgeLabels: false,
  arrows: true,
}

const STORAGE_KEY = 'graph-explorer:display-settings'

// Load the persisted settings, falling back to defaults for a missing/corrupt
// entry or any unknown field. Never throws (storage may be blocked).
export function loadDisplaySettings(): GraphDisplaySettings {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return { ...DEFAULT_DISPLAY_SETTINGS }
    const parsed = JSON.parse(raw) as Partial<GraphDisplaySettings>
    return {
      nodeLabels:
        typeof parsed.nodeLabels === 'boolean'
          ? parsed.nodeLabels
          : DEFAULT_DISPLAY_SETTINGS.nodeLabels,
      edgeLabels:
        typeof parsed.edgeLabels === 'boolean'
          ? parsed.edgeLabels
          : DEFAULT_DISPLAY_SETTINGS.edgeLabels,
      arrows:
        typeof parsed.arrows === 'boolean' ? parsed.arrows : DEFAULT_DISPLAY_SETTINGS.arrows,
    }
  } catch {
    return { ...DEFAULT_DISPLAY_SETTINGS }
  }
}

// Persist the settings. No-op when storage is unavailable.
export function saveDisplaySettings(settings: GraphDisplaySettings): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(settings))
  } catch {
    /* storage full / blocked — preference simply won't persist */
  }
}
