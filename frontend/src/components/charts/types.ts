// Shared data shapes for the Bellu chart wrappers.

/** One category/value pair, e.g. { label: 'Critical', value: 12 }. */
export interface CategoryDatum {
  label: string
  value: number
  /** Optional explicit color (CSS color or var); otherwise the palette is used. */
  color?: string
}

/** One row in a multi-series time/category chart, e.g. { x: 'Mon', openai: 40, anthropic: 25 }. */
export interface SeriesDatum {
  x: string
  [seriesKey: string]: string | number
}
