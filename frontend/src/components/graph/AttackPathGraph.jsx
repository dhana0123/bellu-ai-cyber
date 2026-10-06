import React, { useMemo } from 'react'
import ReactFlow, { Background, Controls, Position } from 'reactflow'
import 'reactflow/dist/style.css'
import dagre from 'dagre'
import GraphNode, { NODE_W, NODE_H } from './GraphNode.jsx'

const nodeTypes = { entity: GraphNode }

// Edge colors by relation (Wiz-style): network=blue, permission=violet/green,
// build/depends=sky, vuln/admin=red.
const EDGE_COLOR = {
  RUNS_IN: '#f59e0b',
  USES: '#a78bfa',
  AUTHENTICATES_AS: '#a78bfa',
  CAN_READ: '#34d399',
  CAN_WRITE: '#fbbf24',
  CAN_ADMIN: '#f87171',
  CONNECTS_TO: '#60a5fa',
  BUILT_FROM: '#38bdf8',
  HAS_VULN: '#f87171',
  default: '#6b7280',
}

// layout runs Dagre left-to-right and writes positions + handle sides back onto
// the nodes so edges always enter the left and leave the right.
function layout(nodes, edges) {
  const g = new dagre.graphlib.Graph()
  g.setDefaultEdgeLabel(() => ({}))
  g.setGraph({ rankdir: 'LR', nodesep: 50, ranksep: 110, marginx: 24, marginy: 24 })

  nodes.forEach((n) => g.setNode(n.id, { width: NODE_W, height: NODE_H }))
  edges.forEach((e) => g.setEdge(e.source, e.target))
  dagre.layout(g)

  const laidOut = nodes.map((n) => {
    const p = g.node(n.id)
    return {
      ...n,
      sourcePosition: Position.Right,
      targetPosition: Position.Left,
      // Dagre gives center; React Flow wants top-left.
      position: { x: p.x - NODE_W / 2, y: p.y - NODE_H / 2 },
    }
  })
  return laidOut
}

function buildGraph(paths, calls) {
  const nodes = new Map()
  const edges = new Map()

  const node = (key, kind, label) => {
    if (!nodes.has(key)) {
      nodes.set(key, { id: key, type: 'entity', position: { x: 0, y: 0 }, data: { kind, label } })
    }
    return key
  }
  const edge = (src, dst, relation) => {
    const id = `${src}__${relation}__${dst}`
    if (edges.has(id)) return
    const color = EDGE_COLOR[relation] || EDGE_COLOR.default
    edges.set(id, {
      id,
      source: src,
      target: dst,
      // Smooth bezier curves like the reference.
      type: 'default',
      label: relation,
      labelShowBg: true,
      labelStyle: { fill: '#d4d4d8', fontSize: 9, fontWeight: 500 },
      labelBgPadding: [4, 2],
      labelBgBorderRadius: 4,
      labelBgStyle: { fill: '#2d2d2d', fillOpacity: 0.95 },
      style: { stroke: color, strokeWidth: 2 },
    })
  }

  const internet = 'node:internet'

  for (const p of paths || []) {
    node(internet, 'Internet', 'Internet')
    const agent = node(`agent:${p.agent}`, 'AIAgent', p.agent)
    const wl = node(`wl:${p.workload}`, 'Workload', p.workload)
    const sa = node(`sa:${p.service_account}`, 'Identity', p.service_account)
    const cloud = node(`id:${p.cloud_identity}`, 'Identity', p.cloud_identity)
    const ds = node(`ds:${p.datastore}`, 'DataStore', p.datastore)
    edge(internet, agent, 'CONNECTS_TO')
    edge(agent, wl, 'RUNS_IN')
    edge(wl, sa, 'USES')
    edge(sa, cloud, 'AUTHENTICATES_AS')
    edge(cloud, ds, p.access || 'CAN_READ')
  }

  for (const c of calls || []) {
    const wl = node(`wl:${c.workload}`, 'Workload', c.workload)
    const svc = node(`svc:${c.endpoint}`, 'ExternalService', c.ai_service || c.endpoint)
    if (c.agent) {
      const agent = node(`agent:${c.agent}`, 'AIAgent', c.agent)
      node(internet, 'Internet', 'Internet')
      edge(internet, agent, 'CONNECTS_TO')
      edge(agent, wl, 'RUNS_IN')
    }
    edge(wl, svc, 'CONNECTS_TO')
  }

  return { nodes: layout([...nodes.values()], [...edges.values()]), edges: [...edges.values()] }
}

export default function AttackPathGraph({ paths, calls }) {
  const { nodes, edges } = useMemo(() => buildGraph(paths, calls), [paths, calls])

  if (nodes.length === 0) {
    return <p className="text-sm text-muted-foreground">No attack paths to visualize.</p>
  }

  return (
    <div className="h-[520px] w-full overflow-hidden rounded-lg border" style={{ background: '#1a1a1a' }}>
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        fitView
        fitViewOptions={{ padding: 0.18 }}
        proOptions={{ hideAttribution: true }}
        nodesConnectable={false}
        minZoom={0.3}
        maxZoom={1.75}
      >
        <Background color="#333" gap={22} size={1} />
        <Controls
          showInteractive={false}
          className="!border-border [&>button]:!border-border [&>button]:!bg-[#2d2d2d] [&>button:hover]:!bg-[#3a3a3a] [&>button]:!fill-foreground"
        />
      </ReactFlow>
    </div>
  )
}
