// The five Bellu chart colors map to the --chart-1..5 CSS variables defined in
// styles.css (light + dark). Using the var() form keeps charts theme-aware.
export const CHART_PALETTE = [
  'hsl(var(--chart-1))',
  'hsl(var(--chart-2))',
  'hsl(var(--chart-3))',
  'hsl(var(--chart-4))',
  'hsl(var(--chart-5))',
] as const

/** Pick a palette color by index, wrapping around. */
export function paletteColor(i: number): string {
  return CHART_PALETTE[i % CHART_PALETTE.length]
}
