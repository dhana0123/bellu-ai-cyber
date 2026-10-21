import { Handle, Position, type NodeProps } from 'reactflow'

import { styleForKind } from '@/features/graph-explorer/lib/iconRegistry'

export interface GraphNodeData {
  kind: string
  label: string
  /** When true, the node is rendered with a pulsing glow (the focused item). */
  highlight?: boolean
}

// Fixed footprint; MUST match the layout math in TraceGraph.tsx.
export const NODE_W = 160
export const NODE_H = 96
const ICON = 46 // icon circle diameter; handles align to its vertical center

const dotStyle = (color: string, side: 'left' | 'right'): React.CSSProperties => ({
  top: ICON / 2,
  [side]: (NODE_W - ICON) / 2 - 4,
  width: 8,
  height: 8,
  borderRadius: 9999,
  background: color,
  border: '2px solid hsl(var(--background))',
})

export default function GraphNode({ data }: NodeProps<GraphNodeData>) {
  const style = styleForKind(data.kind)
  const Icon = style.icon
  return (
    <div
      className="relative flex flex-col items-center text-center"
      style={{ width: NODE_W, height: NODE_H }}
    >
      <Handle type="target" position={Position.Left} style={dotStyle(style.colorCss, 'left')} />

      <div
        className={
          'flex items-center justify-center rounded-full bg-card' +
          (data.highlight ? ' graph-node-pulse' : '')
        }
        style={{
          width: ICON,
          height: ICON,
          border: `2px solid ${style.colorCss}`,
          // When highlighted the keyframe animation drives box-shadow + transform,
          // so we don't set an inline box-shadow (it would override the animation).
          ...(data.highlight
            ? {}
            : { boxShadow: `0 0 0 5px ${style.colorCss}14, 0 0 18px ${style.colorCss}22` }),
          // CSS var feeds the keyframe glow color (see graph-node-pulse).
          ['--pulse-color' as string]: style.colorCss,
        }}
      >
        <Icon style={{ width: 20, height: 20, color: style.colorCss }} />
      </div>

      <div className="mt-2 leading-tight">
        <div
          className="mx-auto max-w-[156px] truncate text-xs font-medium text-foreground"
          title={data.label}
        >
          {data.label}
        </div>
        <div className="text-[10px] text-muted-foreground">{style.label}</div>
      </div>

      <Handle type="source" position={Position.Right} style={dotStyle(style.colorCss, 'right')} />
    </div>
  )
}
