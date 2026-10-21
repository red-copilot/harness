import type { HypothesisView } from './exploration'

export type GraphNodeKind = 'origin' | 'frontier' | 'fact'
export type GraphEdgeTone = 'root' | 'planned' | 'derived' | 'fact'

export interface GraphNode {
  id: string
  kind: GraphNodeKind
  depth: number
  x: number
  y: number
  width: number
  height: number
  status: string
  active: boolean
  round?: number
  frontierId?: string
  factId?: string
}

export interface GraphEdge {
  id: string
  from: string
  to: string
  tone: GraphEdgeTone
}

export interface ExplorationGraphLayout {
  nodes: GraphNode[]
  edges: GraphEdge[]
  width: number
  height: number
}

const ORIGIN = { width: 240, height: 152 }
const FRONTIER = { width: 300, height: 196 }
const FACT = { width: 260, height: 116 }
const COLUMN_GAP = 88
const ROW_GAP = 26
const PADDING = 28
const MIN_HEIGHT = 460

export const ORIGIN_NODE_ID = 'origin'

// buildExplorationGraph turns the bounded blackboard projection into a
// deterministic left-to-right layout. Depth is derived from the round, so the
// board grows to the right as rounds advance and never reshuffles old nodes.
export function buildExplorationGraph(view: HypothesisView): ExplorationGraphLayout {
  const frontierNodes = view.frontiers.map((frontier) => ({
    id: frontier.id,
    kind: 'frontier' as const,
    depth: Math.max(1, 2 * (frontier.round ?? 1) - 1),
    x: 0,
    y: 0,
    width: FRONTIER.width,
    height: FRONTIER.height,
    status: frontier.status || 'open',
    active: view.current.includes(frontier.id),
    round: frontier.round,
    frontierId: frontier.id,
  }))

  const factNodes = view.facts.map((fact) => ({
    id: factNodeId(fact.id),
    kind: 'fact' as const,
    depth: Math.max(2, 2 * (fact.round ?? 1)),
    x: 0,
    y: 0,
    width: FACT.width,
    height: FACT.height,
    status: fact.trust || 'observed',
    active: false,
    round: fact.round,
    factId: fact.id,
  }))

  const nodes: GraphNode[] = [
    {
      id: ORIGIN_NODE_ID,
      kind: 'origin',
      depth: 0,
      x: 0,
      y: 0,
      width: ORIGIN.width,
      height: ORIGIN.height,
      status: originStatus(view),
      active: true,
    },
    ...frontierNodes,
    ...factNodes,
  ]

  const byId = new Map(nodes.map((node) => [node.id, node]))
  const edges = buildEdges(view, byId)
  layoutColumns(nodes)
  return { nodes, edges, width: canvasWidth(nodes), height: canvasHeight(nodes) }
}

function buildEdges(view: HypothesisView, byId: Map<string, GraphNode>): GraphEdge[] {
  const edges: GraphEdge[] = []
  const seen = new Set<string>()
  const push = (from: string, to: string, tone: GraphEdgeTone) => {
    if (!byId.has(from) || !byId.has(to) || from === to) return
    const id = `${from}->${to}`
    if (seen.has(id)) return
    seen.add(id)
    edges.push({ id, from, to, tone })
  }

  for (const frontier of view.frontiers) {
    const parent = frontier.parentId && byId.has(frontier.parentId) ? frontier.parentId : undefined
    if (parent) {
      push(parent, frontier.id, 'derived')
    } else if (frontier.anchors.length) {
      let linked = false
      for (const anchor of frontier.anchors) {
        const anchorID = factNodeId(anchor)
        if (byId.has(anchorID)) {
          push(anchorID, frontier.id, 'derived')
          linked = true
        }
      }
      if (!linked) push(ORIGIN_NODE_ID, frontier.id, 'root')
    } else if ((frontier.round ?? 1) <= 1) {
      push(ORIGIN_NODE_ID, frontier.id, 'root')
    } else {
      push(ORIGIN_NODE_ID, frontier.id, 'planned')
    }
  }

  for (const fact of view.facts) {
    const source = byId.has(fact.sourceItemId) ? fact.sourceItemId : ORIGIN_NODE_ID
    push(source, factNodeId(fact.id), 'fact')
  }

  return edges
}

function layoutColumns(nodes: GraphNode[]): void {
  const columns = new Map<number, GraphNode[]>()
  for (const node of nodes) {
    const column = columns.get(node.depth) ?? []
    column.push(node)
    columns.set(node.depth, column)
  }
  const depthOrder = [...columns.keys()].sort((a, b) => a - b)
  const columnWidths = new Map<number, number>()
  for (const depth of depthOrder) {
    const column = columns.get(depth)!
    columnWidths.set(depth, Math.max(...column.map((node) => node.width)))
  }
  let x = PADDING
  for (const depth of depthOrder) {
    const column = columns.get(depth)!
    for (const node of column) {
      node.x = x + (columnWidths.get(depth)! - node.width) / 2
    }
    x += columnWidths.get(depth)! + COLUMN_GAP
  }
  const height = canvasHeight(nodes)
  for (const depth of depthOrder) {
    const column = columns.get(depth)!
    const stack = column.length * ROW_GAP + column.reduce((total, node) => total + node.height, 0)
    let y = PADDING + Math.max(0, (height - 2 * PADDING - stack) / 2)
    for (const node of column) {
      node.y = y
      y += node.height + ROW_GAP
    }
  }
}

function canvasWidth(nodes: GraphNode[]): number {
  return nodes.reduce((max, node) => Math.max(max, node.x + node.width), 0) + PADDING
}

function canvasHeight(nodes: GraphNode[]): number {
  const columns = new Map<number, number>()
  for (const node of nodes) {
    const stack = (columns.get(node.depth) ?? 0) + node.height + ROW_GAP
    columns.set(node.depth, stack)
  }
  let tallest = 0
  for (const stack of columns.values()) {
    tallest = Math.max(tallest, stack - ROW_GAP)
  }
  return Math.max(MIN_HEIGHT, tallest + 2 * PADDING)
}

function originStatus(view: HypothesisView): string {
  const progress = view.progress
  if (progress && progress.total > 0 && progress.current >= progress.total) return 'done'
  return 'running'
}

export function factNodeId(factId: string): string {
  return `fact:${factId}`
}

// edgePath draws a horizontal cubic bezier between two node boxes.
export function edgePath(from: GraphNode, to: GraphNode): string {
  const startX = from.x + from.width
  const startY = from.y + from.height / 2
  const endX = to.x
  const endY = to.y + to.height / 2
  const control = Math.max(28, Math.abs(endX - startX) / 2)
  return `M ${startX} ${startY} C ${startX + control} ${startY}, ${endX - control} ${endY}, ${endX} ${endY}`
}
