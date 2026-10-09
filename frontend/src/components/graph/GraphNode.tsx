import { Handle, Position, type NodeProps } from 'reactflow'
import {
  Globe,
  Bot,
  Boxes,
  KeyRound,
  Database,
  Cloud,
  Server,
  Bug,
  ShieldAlert,
  Cpu,
  Network,
  Box,
  GitBranch,
  Package,
  type LucideIcon,
} from 'lucide-react'

export interface GraphNodeData {
  kind: string
  label: string
}

interface KindStyle {
  icon: LucideIcon
  color: string
  sub: string
}

// Icon + accent color per canonical kind. Keys match the kinds the graph
// builder emits; preserved exactly from the pre-migration component.
const KIND_STYLE: Record<string, KindStyle> = {
  Internet: { icon: Globe, color: '#3b82f6', sub: 'Internet' },
  AIAgent: { icon: Bot, color: '#f59e0b', sub: 'AI Agent' },
  Workload: { icon: Boxes, color: '#8b5cf6', sub: 'Workload' },
  Identity: { icon: KeyRound, color: '#8b5cf6', sub: 'Identity' },
  DataStore: { icon: Database, color: '#10b981', sub: 'Data Store' },
  CloudAccount: { icon: Cloud, color: '#3b82f6', sub: 'Cloud Account' },
  Compute: { icon: Server, color: '#3b82f6', sub: 'Compute' },
  Cluster: { icon: Network, color: '#3b82f6', sub: 'Cluster' },
  ContainerImage: { icon: Box, color: '#06b6d4', sub: 'Container Image' },
  Repository: { icon: GitBranch, color: '#06b6d4', sub: 'Repository' },
  Package: { icon: Package, color: '#06b6d4', sub: 'Package' },
  Vulnerability: { icon: Bug, color: '#ef4444', sub: 'Vulnerability' },
  AIService: { icon: Cpu, color: '#f59e0b', sub: 'AI Service' },
  ExternalService: { icon: Cloud, color: '#ef4444', sub: 'External Service' },
  MCPServer: { icon: Server, color: '#f59e0b', sub: 'MCP Server' },
  default: { icon: ShieldAlert, color: '#9ca3af', sub: 'Resource' },
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
  const style = KIND_STYLE[data.kind] || KIND_STYLE.default
  const Icon = style.icon
  return (
    <div
      className="relative flex flex-col items-center text-center"
      style={{ width: NODE_W, height: NODE_H }}
    >
      <Handle type="target" position={Position.Left} style={dotStyle(style.color, 'left')} />

      <div
        className="flex items-center justify-center rounded-full bg-card"
        style={{
          width: ICON,
          height: ICON,
          border: `2px solid ${style.color}`,
          boxShadow: `0 0 0 5px ${style.color}14, 0 0 18px ${style.color}22`,
        }}
      >
        <Icon style={{ width: 20, height: 20, color: style.color }} />
      </div>

      <div className="mt-2 leading-tight">
        <div
          className="mx-auto max-w-[156px] truncate text-xs font-medium text-foreground"
          title={data.label}
        >
          {data.label}
        </div>
        <div className="text-[10px] text-muted-foreground">{style.sub}</div>
      </div>

      <Handle type="source" position={Position.Right} style={dotStyle(style.color, 'right')} />
    </div>
  )
}
