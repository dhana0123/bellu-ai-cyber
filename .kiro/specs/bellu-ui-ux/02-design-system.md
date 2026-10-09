# 02 — Design System

The Bellu frontend uses **official shadcn/ui** (Radix primitives) and **official shadcn
Charts** (Recharts), themed to Bellu's "ChatGPT-style" palette. This document is the
reference for tokens, typography, components, and chart usage. It describes what is actually
implemented in `frontend/src`.

---

## 1. Foundations

### 1.1 Stack

| Concern | Choice |
|---|---|
| Framework | React 18 + TypeScript |
| Bundler | Vite 5 (`@` → `src` alias) |
| Styling | Tailwind CSS 3 (`darkMode: ['class']`) + `tailwindcss-animate` |
| Components | shadcn/ui (new-york style) on Radix |
| Charts | shadcn Charts on Recharts (no custom SVG) |
| Icons | lucide-react |
| Class merge | `cn()` = `twMerge(clsx(...))` (`@/lib/utils`) |

### 1.2 Design tokens (CSS variables)

Tokens live in `src/styles.css` under `:root` (light) and `.dark` (dark), mapped to
Tailwind utilities in `tailwind.config.js` via `hsl(var(--x))`. The dark theme emulates
OpenAI ChatGPT.

| Token | Light | Dark (ChatGPT) | Use |
|---|---|---|---|
| `--background` | white | `#212121` | app surface |
| `--foreground` | near-black | `#ededed` | primary text |
| `--card` | white | `#212121` (= background) | cards — separated by border, not fill |
| `--popover` | white | `#2f2f2f` | menus, selects, tooltips (elevated) |
| `--primary` | blue | white | primary action (white pill in dark) |
| `--primary-foreground` | white | near-black | text on primary |
| `--secondary` / `--muted` | light gray | `#2d2d2d` | subtle surfaces |
| `--muted-foreground` | gray | `#b0b0b0` | secondary text |
| `--accent` | light gray | `#303030` | hover |
| `--destructive` | red | red | errors / danger |
| `--border` / `--input` | light gray | `#3d3d3d` | borders |
| `--ring` | blue | neutral gray | focus ring |
| `--radius` | `0.5rem` | — | base radius (`lg/md/sm` derive) |
| `--chart-1..5` | multi | green-led (`#10a37f`-ish + blues/violet/amber/red) | chart series |

Special rule: `.dark .cg-sidebar { background-color: #171717 }` makes the sidebar darker
than the surface. Applied to the `AppShell` sidebar `<aside>` and the mobile Sheet.

Green (`#10a37f`) is a **secondary accent**, not the primary. The primary action is the
white pill (dark theme) / blue (light theme).

### 1.3 Typography & spacing

- Font: inherits the system/Tailwind default sans stack.
- Page title: `text-2xl font-semibold tracking-tight`.
- Section/card title: `text-base`–`text-lg font-semibold`.
- Body: `text-sm`; secondary: `text-xs text-muted-foreground`.
- Monospace (`font-mono`) for IDs, ARNs, hashes, timestamps.
- Page padding: `p-4 md:p-6`; vertical rhythm: `space-y-4`/`space-y-6`.

### 1.4 Theming mechanism

`ThemeProvider` (`@/theme`) stores the theme in `localStorage['bellu-theme']`, toggles the
`light`/`dark` class on `<html>`, and exposes `useTheme()`. Default is `dark`. The topbar
toggles it.

---

## 2. Component inventory (`src/components/ui`)

Official shadcn primitives, Bellu-tuned. Variant/prop names were preserved from the
pre-migration kit so existing usage is unchanged.

| Component | Notes |
|---|---|
| `Button` | `cva` variants `default` (white pill), `primary`, `destructive`, `outline`, `secondary`, `ghost`, `link`; sizes `default/sm/lg/icon`; `asChild` via Radix `Slot`; exports `buttonVariants`. |
| `Badge` | variants `default`, `secondary`, `outline`, **`ai`**, **`success`**, **`warning`**, **`danger`** (Bellu-custom; uses `danger`, not shadcn's `destructive`); exports `badgeVariants`. |
| `Card` + `CardHeader/Title/Description/Content/Footer` | `card === background`; separation via border. |
| `Input`, `Label` (Radix) | |
| `Select` (Radix) | **Replaced the pre-migration native `<select>`.** API: `Select value onValueChange` + `SelectTrigger/SelectValue/SelectContent/SelectItem`. Empty string is not a valid item value — use a sentinel (`__all__`) for "All" options. |
| `Table` + `TableHeader/Body/Row/Head/Cell/Caption` | short aliases `THead/TBody/TR/TH/TD` re-exported for convenience. |
| `Dialog`, `Sheet` | overlays; Sheet drives the mobile sidebar. |
| `DropdownMenu`, `Tabs`, `Tooltip`, `Separator`, `Skeleton` | |
| `Spinner` | loading indicator (not a shadcn primitive). |
| `Toaster` (sonner) | theme read from the `dark` class; mounted once in `main.tsx`. |

### 2.1 Shared app components (`src/components/common`)

| Component | Purpose |
|---|---|
| `AppShell` | sidebar + topbar chrome; permission-filtered config-driven nav; Preview-badged sections; mobile Sheet sidebar. |
| `PreviewBadge` | persistent "Preview — demo data" marker (warning badge + flask icon). Appears on every add-on module. |
| `ModulePlaceholder` | scaffolding placeholder; `preview` flag toggles the Preview badge/copy (Settings uses `preview={false}`). |

### 2.2 Add-on shell (`src/features/addons`)

| Component | Purpose |
|---|---|
| `AddonHeader` | title + blurb + persistent `PreviewBadge`, optional actions. |
| `DataState<T>` | render-prop that maps a `useAsync` state to loading / error / empty / populated UI consistently. |

---

## 3. Charts (`src/components/charts`)

Built on the official shadcn `chart` primitive (`ChartContainer`, `ChartConfig`,
`ChartTooltip`/`ChartTooltipContent`, `ChartLegend`/`ChartLegendContent`) and Recharts.
**No custom SVG charts.** All wrappers are `Card`-wrapped, responsive, themed to
`--chart-1..5`, with consistent tooltips and legends.

| Wrapper | Shape | Use |
|---|---|---|
| `BarChartCard` | `CategoryDatum[]` `{label,value,color?}` | distributions (findings-by-severity, entities-by-kind). `horizontal` for long labels. Per-bar color via `Cell`. |
| `AreaChartCard` | `SeriesDatum[]` + `SeriesDef[]` | stacked/overlaid time series (gateway traffic by provider). |
| `LineChartCard` | same as Area | trend lines (latency, QPS). |
| `DonutChartCard` | `CategoryDatum[]` | compositional mixes with a center total (media classification). |
| `RadialChartCard` | single `0..100` value | gauges (authenticity confidence, risk posture). |

Palette: `CHART_PALETTE` = `hsl(var(--chart-1..5))`; `paletteColor(i)` wraps around. All
chart data in Preview modules is clearly labeled DEMO data.

---

## 4. Interaction & accessibility

- **Keyboard & focus:** Radix primitives provide focus management, roving tabindex, and
  escape-to-close. Buttons/inputs show a visible `focus-visible:ring-2 ring-ring`.
- **Color contrast:** text tokens target WCAG AA against their surfaces; severity/status
  colors are paired with text labels (never color alone).
- **Loading:** `Spinner` and `Skeleton` for pending states; charts render inside responsive
  containers to avoid layout shift.
- **Dark/light:** every token has both themes; charts re-theme via CSS variables.
- Full WCAG conformance requires manual testing with assistive technologies and expert
  review; this system provides the accessible primitives and patterns.

---

## 5. Conventions

- Pages live in `src/features/<area>/`; shared UI in `src/components/`.
- Data always flows through `@/lib/client` (foundation → real API, add-ons → mocks).
- `useAsync(fn, deps)` for reads; `error` is a message string.
- Permission-gate routes with `RequireAuth`; gate nav items with a `permission` string.
- Prefer the shared `DataState` + `AddonHeader` for add-on pages to keep states uniform.
