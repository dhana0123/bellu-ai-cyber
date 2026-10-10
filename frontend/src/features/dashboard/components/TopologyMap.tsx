import { useEffect, useRef, useState } from 'react'
import { Area, AreaChart, ResponsiveContainer, XAxis } from 'recharts'
import { X } from 'lucide-react'
import { SEVERITY_HSL } from './severity'
import type { DashboardData, SeverityMetric } from '@/types/dashboard'
import type { Severity } from '@/types/foundation'
import '../topology.css'

// Fixed coordinate world; the viewport pans/zooms over it.
const WORLD_W = 1400
const WORLD_H = 760

// Zone anchors (center points) in world coordinates.
const HUB = { x: 700, y: 380 }
const LEFT_X = 230
// Four orbs — one per severity — evenly spaced on the left.
const ORB_Y = [120, 290, 460, 630]
const TREE_X = 1030

// Reuse the dashboard's severity hues so the orbs match the Grid view exactly.
const ORB_COLOR = SEVERITY_HSL

// ---- Left risk orbs --------------------------------------------------------
function RiskOrb({
  value,
  label,
  trend,
  trendUpIsBad,
  color,
  glow,
  pos,
  active,
  onClick,
}: {
  value: number
  label: string
  trend: string
  trendUpIsBad: boolean
  color: string
  glow?: boolean
  pos: { x: number; y: number }
  active?: boolean
  onClick?: () => void
}) {
  const up = trend.includes('↑') || trend.trim().startsWith('+')
  const good = trendUpIsBad ? !up : up
  // color-mix keeps translucency working with hsl() color strings.
  const faint = `color-mix(in srgb, ${color} 18%, transparent)`
  return (
    <div
      data-topo-interactive
      role="button"
      tabIndex={0}
      aria-pressed={active}
      onClick={onClick}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') onClick?.()
      }}
      className="absolute flex -translate-x-1/2 -translate-y-1/2 cursor-pointer flex-col items-center outline-none"
      style={{ left: pos.x, top: pos.y }}
    >
      <span
        className={`absolute -top-4 z-10 rounded-full px-2 py-0.5 text-[11px] font-semibold ${
          good ? 'bg-emerald-500/20 text-emerald-300' : 'bg-red-500/20 text-red-300'
        }`}
      >
        {trend}
      </span>
      <div
        className={`flex h-24 w-24 items-center justify-center rounded-full border-2 text-3xl font-bold tabular-nums transition-transform hover:scale-105 ${glow ? 'topo-pulse-red' : ''}`}
        style={{
          borderColor: color,
          background: `radial-gradient(circle at 50% 35%, ${faint}, #0e141a 70%)`,
          color: '#fff',
          boxShadow: active
            ? `0 0 0 4px color-mix(in srgb, ${color} 35%, transparent), 0 0 26px ${faint}`
            : glow
              ? undefined
              : `0 0 18px ${faint}`,
        }}
      >
        {value}
      </div>
      <span className="mt-2 whitespace-nowrap text-xs text-slate-300">{label}</span>
    </div>
  )
}

// ---- Center hub with 29% SVG arc + hover history chart ---------------------
function CenterHub({
  score,
  title,
  trend,
  history,
}: {
  score: number
  title: string
  trend: string
  history: { x: string; value: number }[]
}) {
  const [hover, setHover] = useState(false)
  const R = 70
  const C = 2 * Math.PI * R
  const dash = (score / 100) * C

  return (
    <div
      data-topo-interactive
      className="absolute -translate-x-1/2 -translate-y-1/2"
      style={{ left: HUB.x, top: HUB.y }}
      onMouseEnter={() => setHover(true)}
      onMouseLeave={() => setHover(false)}
    >
      <div className="relative flex h-48 w-48 items-center justify-center">
        <svg width={192} height={192} className="absolute inset-0 -rotate-90">
          <circle cx={96} cy={96} r={R} fill="none" stroke="#1b2430" strokeWidth={10} />
          <circle
            cx={96}
            cy={96}
            r={R}
            fill="none"
            stroke="#10a37f"
            strokeWidth={10}
            strokeLinecap="round"
            strokeDasharray={`${dash} ${C - dash}`}
          />
        </svg>
        <div
          className="flex h-32 w-32 flex-col items-center justify-center rounded-full border border-emerald-500/30 text-center"
          style={{ background: 'radial-gradient(circle at 50% 30%, #11202099, #0b1116)' }}
        >
          <span className="font-mono text-4xl font-bold text-emerald-300">{score}%</span>
          <span className="mt-1 inline-flex items-center gap-1 rounded-full bg-emerald-500/15 px-1.5 text-[11px] text-emerald-300">
            {trend}
          </span>
          <span className="mt-1 max-w-[7rem] text-[10px] leading-tight text-slate-400">
            {title}
          </span>
        </div>
      </div>

      {/* Glassmorphic hover tooltip: 30-day score history */}
      {hover && (
        <div className="absolute left-1/2 top-[-170px] z-20 w-72 -translate-x-1/2 rounded-xl border border-white/10 bg-white/5 p-3 shadow-2xl backdrop-blur-md">
          <div className="mb-1 text-xs font-medium text-slate-200">Security score — 30 days</div>
          <ResponsiveContainer width="100%" height={110}>
            <AreaChart data={history} margin={{ left: 0, right: 0, top: 6, bottom: 0 }}>
              <defs>
                <linearGradient id="topo-hub-grad" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0%" stopColor="#10a37f" stopOpacity={0.5} />
                  <stop offset="100%" stopColor="#10a37f" stopOpacity={0.02} />
                </linearGradient>
              </defs>
              <XAxis
                dataKey="x"
                tick={{ fill: '#8b98a5', fontSize: 10 }}
                axisLine={false}
                tickLine={false}
                interval="preserveStartEnd"
              />
              <Area
                dataKey="value"
                type="monotone"
                stroke="#10a37f"
                strokeWidth={2}
                fill="url(#topo-hub-grad)"
                dot={false}
              />
            </AreaChart>
          </ResponsiveContainer>
        </div>
      )}
    </div>
  )
}

// ---- Mini semi-circular SLA gauge for tree hover ---------------------------
function SlaGauge({ age, sla }: { age: number; sla: number }) {
  const ceiling = Math.max(age, sla)
  const frac = ceiling > 0 ? Math.min(1, age / ceiling) : 0
  const breached = age > sla
  const R = 34
  const C = Math.PI * R // semicircle
  const dash = frac * C
  const color = breached ? '#ef4444' : '#10a37f'
  return (
    <div className="flex flex-col items-center">
      <svg width={90} height={52} viewBox="0 0 90 52">
        <path d="M 11 48 A 34 34 0 0 1 79 48" fill="none" stroke="#1b2430" strokeWidth={8} />
        <path
          d="M 11 48 A 34 34 0 0 1 79 48"
          fill="none"
          stroke={color}
          strokeWidth={8}
          strokeLinecap="round"
          strokeDasharray={`${dash} ${C - dash}`}
        />
        <text x={45} y={44} textAnchor="middle" className="fill-white text-sm font-bold">
          {age}d
        </text>
      </svg>
      <div className="mt-1 text-[11px] text-slate-300">
        Age: {age} Days / Target SLA: {sla} Days
      </div>
    </div>
  )
}

// Right-side rule strings (exact spec text), mapped by index to the age/SLA data.
const TREE_RULES = [
  'Internet-facing container using an image with initial access vulnerabilities...',
  'Internet-facing VM/serverless with initial access vulnerabilities and data access...',
  'Internet-facing VM/serverless with initial access vulnerabilities can access a bucket...',
  'Exposed vulnerable software on VM/serverless with sensitive data access',
]

function ImpactTree({ gauges, hidden }: { gauges: DashboardData['issue_age']; hidden: boolean }) {
  const [hover, setHover] = useState<number | null>(null)
  const rowH = 70
  const startY = HUB.y - (gauges.length * rowH) / 2 + rowH / 2
  return (
    <div
      className="topo-ease"
      style={{
        opacity: hidden ? 0 : 1,
        transform: hidden ? 'translateX(40px)' : 'translateX(0)',
        pointerEvents: hidden ? 'none' : undefined,
      }}
    >
      <div
        className="absolute -translate-y-1/2 text-xs font-semibold uppercase tracking-wider text-slate-400"
        style={{ left: TREE_X, top: startY - rowH }}
      >
        Exposed Interfaces &amp; SLAs
      </div>
      {gauges.map((g, i) => {
        const y = startY + i * rowH
        const active = hover === i
        return (
          <div
            key={g.severity}
            data-topo-interactive
            className="absolute flex w-[320px] -translate-y-1/2 cursor-pointer items-start gap-2"
            style={{ left: TREE_X, top: y }}
            onMouseEnter={() => setHover(i)}
            onMouseLeave={() => setHover(null)}
          >
            <span
              className={`mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded-full border text-[10px] ${
                active
                  ? 'border-emerald-400 bg-emerald-400 text-[#090d10] shadow-[0_0_10px_#10a37f]'
                  : 'border-slate-500 text-transparent'
              }`}
            >
              ●
            </span>
            <span className={`text-xs leading-snug ${active ? 'text-white' : 'text-slate-400'}`}>
              {TREE_RULES[i] ?? g.label}
            </span>

            {active && (
              <div className="absolute left-[-210px] top-1/2 z-20 -translate-y-1/2 rounded-xl border border-white/10 bg-white/5 p-3 shadow-2xl backdrop-blur-md">
                <SlaGauge age={g.days} sla={g.sla_days} />
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}

// ---- Connector splines (left orbs → hub), carrying top-issue counts --------
// `litSplineId` matches a top-issue id; that spline switches to a glowing neon
// orange stroke (the edge-lighting event triggered from the detail panel).
function Splines({
  issues,
  litSplineId,
}: {
  issues: DashboardData['top_issues']
  litSplineId: string | null
}) {
  const [hover, setHover] = useState<number | null>(null)

  // One spline per left orb; the first three carry a count badge + hover tip for
  // the matching Top Issues rule row.
  const paths = ORB_Y.map((oy, i) => {
    const sx = LEFT_X + 48
    const sy = oy
    const ex = HUB.x - 70
    const ey = HUB.y
    const c1x = sx + 180
    const c2x = ex - 180
    const d = `M ${sx} ${sy} C ${c1x} ${sy}, ${c2x} ${ey}, ${ex} ${ey}`
    const midX = (sx + ex) / 2
    const midY = (sy + ey) / 2 + (sy - ey) * 0.12
    return { d, midX, midY, issue: issues[i] }
  })

  // Big sweeping arc behind the right-hand list (reference's green bloom arc).
  const arcD = `M ${HUB.x + 120} 60 C ${WORLD_W - 120} 180, ${WORLD_W - 120} ${WORLD_H - 180}, ${HUB.x + 120} ${WORLD_H - 60}`

  return (
    <>
      <svg className="absolute inset-0" width={WORLD_W} height={WORLD_H}>
        <defs>
          {/* Soft neon glow for strokes. */}
          <filter id="topo-glow" x="-50%" y="-50%" width="200%" height="200%">
            <feGaussianBlur stdDeviation="4" result="blur" />
            <feMerge>
              <feMergeNode in="blur" />
              <feMergeNode in="SourceGraphic" />
            </feMerge>
          </filter>
          <filter id="topo-glow-strong" x="-80%" y="-80%" width="260%" height="260%">
            <feGaussianBlur stdDeviation="10" result="blur" />
            <feMerge>
              <feMergeNode in="blur" />
              <feMergeNode in="SourceGraphic" />
            </feMerge>
          </filter>
        </defs>

        {/* Big breathing green arc (bloom halo behind the right side). */}
        <path
          d={arcD}
          fill="none"
          stroke="#10a37f"
          strokeWidth={3}
          strokeLinecap="round"
          opacity={0.5}
          filter="url(#topo-glow-strong)"
          className="topo-arc-breathe"
        />

        {paths.map((p, i) => {
          const active = hover === i
          const lit = p.issue != null && litSplineId === p.issue.id
          // When any spline is lit, dim the others for isolation.
          const dimmed = litSplineId != null && !lit
          const stroke = lit ? '#f97316' : '#ef4444'
          return (
            <g key={i}>
              <path d={p.d} fill="none" stroke="#223042" strokeWidth={2} />
              <path
                d={p.d}
                fill="none"
                stroke={stroke}
                strokeWidth={lit ? 4 : active ? 3 : 2}
                className={lit ? 'topo-spline-flow topo-edge-lit' : 'topo-spline-flow'}
                opacity={lit ? 1 : dimmed ? 0.15 : active ? 1 : 0.9}
                filter={lit || active ? 'url(#topo-glow)' : undefined}
              />
              {/* Flowing light particles traveling along the path. */}
              {!dimmed &&
                [0, 1, 2].map((k) => (
                  <circle
                    key={k}
                    r={lit ? 3 : 2}
                    fill={lit ? '#fdba74' : '#fca5a5'}
                    className="topo-particle"
                    style={{
                      offsetPath: `path("${p.d}")`,
                      animationDelay: `${k * 0.9}s`,
                      opacity: lit ? 1 : 0.8,
                    }}
                    filter="url(#topo-glow)"
                  />
                ))}
              {/* Wide invisible hit-path so the thin spline is easy to hover. */}
              {p.issue && (
                <path
                  d={p.d}
                  data-topo-interactive
                  fill="none"
                  stroke="transparent"
                  strokeWidth={22}
                  style={{ cursor: 'pointer' }}
                  onMouseEnter={() => setHover(i)}
                  onMouseLeave={() => setHover((h) => (h === i ? null : h))}
                />
              )}
            </g>
          )
        })}
        {paths.map((p, i) => {
          if (!p.issue) return null
          const lit = litSplineId === p.issue.id
          const dimmed = litSplineId != null && !lit
          return (
            <g
              key={`b-${i}`}
              data-topo-interactive
              style={{ cursor: 'pointer', opacity: dimmed ? 0.3 : 1 }}
              onMouseEnter={() => setHover(i)}
              onMouseLeave={() => setHover((h) => (h === i ? null : h))}
            >
              <circle
                cx={p.midX}
                cy={p.midY}
                r={hover === i || lit ? 15 : 13}
                fill={lit ? '#2a1708' : '#140d0d'}
                stroke={lit ? '#f97316' : '#ef4444'}
                strokeWidth={lit ? 2 : 1.5}
                className={lit ? 'topo-edge-lit' : undefined}
              />
              <text
                x={p.midX}
                y={p.midY + 4}
                textAnchor="middle"
                className={`text-xs font-bold ${lit ? 'fill-orange-200' : 'fill-red-300'}`}
              >
                {p.issue.issues}
              </text>
            </g>
          )
        })}
      </svg>

      {/* Rule-name hover tooltips (HTML overlay positioned at each badge). */}
      {paths.map((p, i) =>
        p.issue && hover === i ? (
          <div
            key={`t-${i}`}
            className="pointer-events-none absolute z-20 w-72 -translate-x-1/2 -translate-y-full rounded-xl border border-white/10 bg-white/5 p-3 shadow-2xl backdrop-blur-md"
            style={{ left: p.midX, top: p.midY - 18 }}
          >
            <div className="mb-1 flex items-center gap-2">
              <span className="rounded-full bg-red-500/20 px-1.5 py-0.5 text-[10px] font-semibold uppercase text-red-300">
                {p.issue.severity}
              </span>
              <span className="text-[11px] text-slate-400">{p.issue.issues} issues</span>
            </div>
            <div className="text-xs leading-snug text-slate-100">{p.issue.rule}</div>
          </div>
        ) : null,
      )}
    </>
  )
}

// ---- Advisory ticker (fixed overlay; not pan/zoomed) -----------------------
function AdvisoryTicker({ advisories }: { advisories: DashboardData['advisories'] }) {
  const [idx, setIdx] = useState(0)
  const [visible, setVisible] = useState(true)

  useEffect(() => {
    if (advisories.length <= 1) return
    const id = setInterval(() => {
      setVisible(false)
      const t = setTimeout(() => {
        setIdx((i) => (i + 1) % advisories.length)
        setVisible(true)
      }, 500)
      return () => clearTimeout(t)
    }, 5000)
    return () => clearInterval(id)
  }, [advisories.length])

  const a = advisories[idx]
  if (!a) return null
  return (
    <div className="pointer-events-none absolute left-1/2 top-4 z-30 -translate-x-1/2">
      <div
        className={`topo-ticker-fade flex items-center gap-2 rounded-full border border-white/10 bg-black/50 px-4 py-2 text-sm text-slate-200 shadow-lg backdrop-blur ${
          visible ? 'opacity-100' : 'opacity-0'
        }`}
      >
        <span className="text-red-500">🔴</span>
        <span className="font-medium text-slate-400">Latest Published Advisory:</span>
        <span className="font-semibold text-white">{a.title}</span>
        <span className="text-slate-400">(Published: {a.published})</span>
      </div>
    </div>
  )
}

// ---- In-canvas vulnerability detail module ---------------------------------
// Borderless + transparent, anchored absolutely to the right sector of the
// viewport (right:80, top:120, w:340, z:40) — never floats over the center hub.
// Slides/fades in over 300ms (cubic-bezier(0.16,1,0.3,1)) into the space the SLA
// tree vacates. Rows are 100% transparent (no card background/border/shadow);
// each has an orange selection node that illuminates its matching spline.
function IssuesPanel({
  severity,
  color,
  metric,
  issues,
  litSplineId,
  onRuleNodeSelect,
  onClose,
}: {
  severity: Severity
  color: string
  metric?: SeverityMetric
  issues: DashboardData['top_issues']
  litSplineId: string | null
  onRuleNodeSelect: (targetSplineId: string) => void
  onClose: () => void
}) {
  const rows = issues.filter((i) => i.severity === severity)
  // Flip after mount so the enter transition (slide-in + fade-in) runs.
  const [entered, setEntered] = useState(false)
  useEffect(() => {
    const id = requestAnimationFrame(() => setEntered(true))
    return () => cancelAnimationFrame(id)
  }, [])

  return (
    <div
      className="topo-ease absolute flex w-[340px] flex-col"
      style={{
        right: 80,
        top: 120,
        zIndex: 40,
        opacity: entered ? 1 : 0,
        transform: entered ? 'translateX(0)' : 'translateX(40px)',
      }}
    >
      {/* Header — transparent, borderless */}
      <div className="flex items-start justify-between gap-3 pb-2">
        <div className="flex items-center gap-2">
          <span className="h-3 w-3 rounded-full" style={{ background: color }} />
          <div>
            <div className="text-sm font-semibold capitalize text-white">{severity} Issues</div>
            <div className="text-xs text-[#8B949E]">
              {metric?.count ?? rows.length} open · {rows.length} top rules
            </div>
          </div>
        </div>
        <button
          onClick={onClose}
          className="flex h-7 w-7 items-center justify-center rounded-md text-[#8B949E] hover:bg-white/10 hover:text-white"
          aria-label="Close issues panel"
        >
          <X className="h-4 w-4" />
        </button>
      </div>

      {/* Scroll region with smoky top/bottom fade caps */}
      <div className="relative">
        {/* Top smoke cap (48px) */}
        <div className="pointer-events-none absolute left-0 top-0 z-20 h-12 w-full bg-gradient-to-b from-[#090D10] to-transparent" />

        <div className="custom-scrollbar-hidden relative max-h-[480px] divide-y divide-white/5 overflow-y-auto bg-transparent py-6">
          {rows.length === 0 ? (
            <p className="py-6 text-center text-sm text-[#8B949E]">
              No top rules for this severity.
            </p>
          ) : (
            rows.map((it) => {
              const lit = litSplineId === it.id
              return (
                // Fully transparent row — no card background, border, or shadow.
                <div key={it.id} className="flex items-start gap-2.5 py-2.5">
                  {/* Orange selection node — illuminates the matching spline */}
                  <button
                    onClick={() => onRuleNodeSelect(it.id)}
                    aria-label={`Illuminate path for ${it.rule}`}
                    aria-pressed={lit}
                    className={`mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded-full border transition-all ${
                      lit
                        ? 'border-orange-400 bg-orange-500 shadow-[0_0_10px_#f97316]'
                        : 'topo-node-pulse border-orange-500/70 bg-orange-500/20 hover:bg-orange-500/40'
                    }`}
                  >
                    <span
                      className={`h-1.5 w-1.5 rounded-full ${lit ? 'bg-white' : 'bg-orange-400'}`}
                    />
                  </button>

                  <div className="min-w-0 flex-1">
                    <div
                      className={`text-[13px] leading-snug ${lit ? 'text-white' : 'text-slate-200'}`}
                    >
                      {it.rule}
                    </div>
                    {/* Muted tag capsules nestled directly under the header */}
                    <div className="mt-1 flex flex-wrap items-center gap-1">
                      <span className="rounded-md bg-white/5 px-2 py-0.5 text-[10px] text-[#8B949E]">
                        {it.issues} Issues
                      </span>
                      {it.risks.map((r) => (
                        <span
                          key={r.id}
                          className="rounded-md bg-white/5 px-2 py-0.5 text-[10px] text-[#8B949E]"
                        >
                          {r.label}
                        </span>
                      ))}
                    </div>
                  </div>
                </div>
              )
            })
          )}
        </div>

        {/* Bottom smoke cap (48px) */}
        <div className="pointer-events-none absolute bottom-0 left-0 z-20 h-12 w-full bg-gradient-to-t from-[#090D10] to-transparent" />
      </div>
    </div>
  )
}

// 2D fluid absolute-coordinate topology canvas. All values are derived from the
// real DashboardData (no placeholders): four left orbs = per-severity open-issue
// counts (dashboard colors), center hub = security score, splines carry the top-
// issue counts, right tree = issue-age/SLA. Clicking an orb slides in a panel
// listing that severity's issues.

// Measures the viewport element and returns a scale that fits the fixed-size
// world inside it. Recomputes on resize, browser zoom, and panel open/close
// (all of which change the element's box) via ResizeObserver.
function useFitScale(worldW: number, worldH: number) {
  const ref = useRef<HTMLDivElement>(null)
  const [scale, setScale] = useState(1)

  useEffect(() => {
    const el = ref.current
    if (!el) return
    const measure = () => {
      const { width, height } = el.getBoundingClientRect()
      if (width === 0 || height === 0) return
      // Contain-fit; never upscale beyond 1:1 so the art stays crisp.
      setScale(Math.min(width / worldW, height / worldH, 1))
    }
    measure()
    const ro = new ResizeObserver(measure)
    ro.observe(el)
    window.addEventListener('resize', measure)
    return () => {
      ro.disconnect()
      window.removeEventListener('resize', measure)
    }
  }, [worldW, worldH])

  return { ref, scale }
}

export function TopologyMap({ data }: { data: DashboardData }) {
  const [selected, setSelected] = useState<Severity | null>(null)
  const [litSplineId, setLitSplineId] = useState<string | null>(null)
  const { ref: viewportRef, scale } = useFitScale(WORLD_W, WORLD_H)

  const byId = (s: Severity) => data.open_issues.find((m) => m.severity === s)

  const selectOrb = (sev: Severity) =>
    setSelected((s) => {
      const next = s === sev ? null : sev
      setLitSplineId(null) // reset edge-lighting when the panel target changes
      return next
    })

  // onRuleNodeSelect — isolate + illuminate the clicked rule's connector spline.
  const onRuleNodeSelect = (targetSplineId: string) =>
    setLitSplineId((id) => (id === targetSplineId ? null : targetSplineId))

  const fmtTrend = (m?: SeverityMetric) =>
    m ? `${m.trend.pct >= 0 ? '↑' : '↓'} ${Math.abs(m.trend.pct)}%` : ''

  // One orb per severity, in display order, using the dashboard severity hues.
  const ORBS: { severity: Severity; glow?: boolean }[] = [
    { severity: 'critical', glow: true },
    { severity: 'high' },
    { severity: 'medium' },
    { severity: 'low' },
  ]

  return (
    <div className="topo-canvas relative h-full min-h-[480px] w-full">
      {/* Static topology area. The fixed-size world auto-scales to fit this box
          (resize, browser zoom). The detail module docks inline at right:340px. */}
      <div
        ref={viewportRef}
        className="relative flex h-full w-full items-center justify-center overflow-hidden"
      >
        <AdvisoryTicker advisories={data.advisories} />

        <div className="topo-grid-dots" />
        <div
          className="topo-ease relative shrink-0"
          style={{
            width: WORLD_W,
            height: WORLD_H,
            // Camera pans slightly left when a panel opens, clearing the right
            // sector for the incoming detail list.
            transform: `translateX(${selected ? -120 : 0}px) scale(${scale})`,
          }}
        >
          <Splines issues={data.top_issues} litSplineId={litSplineId} />

          {ORBS.map((o, i) => {
            const m = byId(o.severity)
            if (!m) return null
            return (
              <RiskOrb
                key={o.severity}
                value={m.count}
                label={m.label}
                trend={fmtTrend(m)}
                trendUpIsBad
                color={ORB_COLOR[o.severity]}
                glow={o.glow}
                pos={{ x: LEFT_X, y: ORB_Y[i] }}
                active={selected === o.severity}
                onClick={() => selectOrb(o.severity)}
              />
            )
          })}

          <CenterHub
            score={data.security_score.score}
            title={data.security_score.source}
            trend={`↑ ${Math.abs(data.security_score.trend.pct)}%`}
            history={data.security_score.history}
          />

          <ImpactTree gauges={data.issue_age} hidden={!!selected} />
        </div>
      </div>

      {/* In-canvas detail module — borderless/transparent, docked at right:380px */}
      {selected && (
        <IssuesPanel
          severity={selected}
          color={ORB_COLOR[selected]}
          metric={byId(selected)}
          issues={data.top_issues}
          litSplineId={litSplineId}
          onRuleNodeSelect={onRuleNodeSelect}
          onClose={() => {
            setSelected(null)
            setLitSplineId(null)
          }}
        />
      )}
    </div>
  )
}
