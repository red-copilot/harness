import type { Artifact, BoardResponse, Claim, Intent, IntentAttempt, Observation } from '@/api/types'

export const EXPLORATION_NODE_WIDTH = 280
export const EXPLORATION_NODE_HEIGHT = 168
export const EXPLORATION_LEVEL_GAP = 96
export const EXPLORATION_ROW_GAP = 28
export const EXPLORATION_PADDING = 28

export interface ExplorationGraphNode {
  id: string
  kind: 'origin' | 'intent'
  level: number
  order: number
  x: number
  y: number
  width: number
  height: number
  origin?: Claim
  intent?: Intent
  fromClaims: Claim[]
  resultClaim?: Claim
  attempts: IntentAttempt[]
  observations: Observation[]
  artifacts: Artifact[]
  unresolvedFrom: string[]
}

export interface ExplorationGraphEdge {
  id: string
  from: string
  to: string
  path: string
  root: boolean
  planned: boolean
}

export interface ExplorationGraphLayout {
  nodes: ExplorationGraphNode[]
  edges: ExplorationGraphEdge[]
  width: number
  height: number
}

/** ExplorationBoard is the subset of the board the layout actually reads. */
export type ExplorationBoard = Pick<BoardResponse, 'claims' | 'intents' | 'attempts' | 'observations' | 'artifacts'>

const ROOT_NODE_ID = '__exploration_origin__'

export function layoutExplorationGraph(board: ExplorationBoard): ExplorationGraphLayout {
  const claims = board.claims ?? []
  const intents = [...(board.intents ?? [])].sort(compareIntents)
  const attempts = board.attempts ?? []
  const observations = board.observations ?? []
  const artifacts = board.artifacts ?? []
  const claimByID = new Map(claims.map((claim) => [claim.id, claim]))
  const origin = claims.find((claim) => claim.kind === 'origin')

  const nodes: ExplorationGraphNode[] = [{
    id: ROOT_NODE_ID,
    kind: 'origin',
    level: 0,
    order: 0,
    x: 0,
    y: 0,
    width: EXPLORATION_NODE_WIDTH,
    height: EXPLORATION_NODE_HEIGHT,
    origin,
    fromClaims: [],
    attempts: [],
    observations: [],
    artifacts: [],
    unresolvedFrom: [],
  }]
  const nodeByID = new Map<string, ExplorationGraphNode>([[ROOT_NODE_ID, nodes[0]]])
  for (const intent of intents) {
    const node: ExplorationGraphNode = {
      id: intent.id,
      kind: 'intent',
      level: 1,
      order: 0,
      x: 0,
      y: 0,
      width: EXPLORATION_NODE_WIDTH,
      height: EXPLORATION_NODE_HEIGHT,
      intent,
      fromClaims: [],
      resultClaim: intent.result_claim_id ? claimByID.get(intent.result_claim_id) : undefined,
      attempts: [],
      observations: [],
      artifacts: [],
      unresolvedFrom: [],
    }
    nodes.push(node)
    nodeByID.set(node.id, node)
  }

  const attemptsByIntent = groupBy(attempts, (attempt) => attempt.intent_id)
  const observationsByAttempt = groupBy(observations, (observation) => observation.attempt_id)
  const artifactsByAttempt = groupBy(artifacts, (artifact) => artifact.attempt_id ?? '')
  for (const node of nodes) {
    if (node.kind !== 'intent' || !node.intent) continue
    node.attempts = attemptsByIntent.get(node.intent.id) ?? []
    node.observations = node.attempts.flatMap((attempt) => observationsByAttempt.get(attempt.id) ?? [])
    node.artifacts = node.attempts.flatMap((attempt) => artifactsByAttempt.get(attempt.id) ?? [])
  }

  const claimNodeID = new Map<string, string>()
  if (origin) claimNodeID.set(origin.id, ROOT_NODE_ID)
  for (const node of nodes) {
    if (node.intent?.result_claim_id) claimNodeID.set(node.intent.result_claim_id, node.id)
  }
  for (const claim of claims) {
    if (!claimNodeID.has(claim.id) && claim.source_intent_id && nodeByID.has(claim.source_intent_id)) {
      claimNodeID.set(claim.id, claim.source_intent_id)
    }
  }

  const parents = new Map<string, Set<string>>()
  const children = new Map<string, Set<string>>()
  for (const node of nodes) {
    parents.set(node.id, new Set())
    children.set(node.id, new Set())
  }
  for (const node of nodes) {
    if (node.kind !== 'intent' || !node.intent) continue
    const sourceIDs = node.intent.from_claim_ids ?? []
    node.fromClaims = sourceIDs.flatMap((id) => {
      const claim = claimByID.get(id)
      return claim ? [claim] : []
    })
    for (const claimID of sourceIDs) {
      const parentID = claimNodeID.get(claimID)
      if (!parentID || parentID === node.id) {
        node.unresolvedFrom.push(claimID)
        continue
      }
      parents.get(node.id)!.add(parentID)
      children.get(parentID)!.add(node.id)
    }
  }

  const levels = assignLevels(nodes, nodeByID, parents, children)
  for (const node of nodes) node.level = levels.get(node.id) ?? 1

  const levelsToNodes = groupBy(nodes, (node) => node.level)
  const orderByID = new Map<string, number>([[ROOT_NODE_ID, 0]])
  const maxLevel = Math.max(0, ...nodes.map((node) => node.level))
  for (let level = 1; level <= maxLevel; level++) {
    const levelNodes = levelsToNodes.get(level) ?? []
    const barycenters = new Map(levelNodes.map((node) => [node.id, parentBarycenter(parents.get(node.id) ?? new Set(), orderByID)]))
    levelNodes.sort((left, right) => {
      const difference = barycenters.get(left.id)! - barycenters.get(right.id)!
      if (difference !== 0) return difference
      return compareIntentNodes(left, right)
    })
    levelNodes.forEach((node, order) => {
      node.order = order
      orderByID.set(node.id, order)
    })
  }

  for (const node of nodes) {
    node.x = EXPLORATION_PADDING + node.level * (EXPLORATION_NODE_WIDTH + EXPLORATION_LEVEL_GAP)
    node.y = EXPLORATION_PADDING + node.order * (EXPLORATION_NODE_HEIGHT + EXPLORATION_ROW_GAP)
  }

  const edges: ExplorationGraphEdge[] = []
  for (const child of nodes) {
    for (const parentID of [...parents.get(child.id)!].sort()) {
      const parent = nodeByID.get(parentID)!
      edges.push({
        id: `${parentID}->${child.id}`,
        from: parentID,
        to: child.id,
        path: edgePath(parent, child),
        root: parent.kind === 'origin',
        planned: child.intent?.state !== 'concluded',
      })
    }
  }
  edges.sort((left, right) => left.id.localeCompare(right.id))

  const maxRows = Math.max(1, ...Array.from(levelsToNodes.values(), (levelNodes) => levelNodes.length))
  const width = EXPLORATION_PADDING * 2 + (maxLevel + 1) * EXPLORATION_NODE_WIDTH + maxLevel * EXPLORATION_LEVEL_GAP
  const height = EXPLORATION_PADDING * 2 + maxRows * EXPLORATION_NODE_HEIGHT + (maxRows - 1) * EXPLORATION_ROW_GAP
  return { nodes, edges, width, height }
}

function assignLevels(
  nodes: ExplorationGraphNode[],
  nodeByID: Map<string, ExplorationGraphNode>,
  parents: Map<string, Set<string>>,
  children: Map<string, Set<string>>,
): Map<string, number> {
  const indegree = new Map(nodes.map((node) => [node.id, parents.get(node.id)?.size ?? 0]))
  const levels = new Map<string, number>(nodes.map((node) => [node.id, node.kind === 'origin' ? 0 : 1]))
  const ready = nodes.filter((node) => indegree.get(node.id) === 0).sort(compareIntentNodes)
  const visited = new Set<string>()
  // A cursor keeps processed nodes in place; new nodes are inserted into the
  // still-sorted unprocessed suffix.
  let cursor = 0
  while (cursor < ready.length) {
    const node = ready[cursor++]
    visited.add(node.id)
    for (const childID of [...(children.get(node.id) ?? [])].sort()) {
      levels.set(childID, Math.max(levels.get(childID) ?? 1, (levels.get(node.id) ?? 0) + 1))
      const next = (indegree.get(childID) ?? 0) - 1
      indegree.set(childID, next)
      if (next === 0) {
        insertSorted(ready, nodeByID.get(childID)!, compareIntentNodes, cursor)
      }
    }
  }
  // Invalid cyclic data must still render deterministically. Keep any node that
  // could not be topologically placed one column after its known parents.
  for (const node of nodes) {
    if (visited.has(node.id)) continue
    const parentLevels = [...(parents.get(node.id) ?? [])].map((id) => levels.get(id) ?? 0)
    levels.set(node.id, Math.max(1, ...parentLevels.map((level) => level + 1)))
  }
  return levels
}

function insertSorted<T>(values: T[], value: T, compare: (left: T, right: T) => number, from: number): void {
  let low = from
  let high = values.length
  while (low < high) {
    const middle = (low + high) >> 1
    if (compare(values[middle], value) <= 0) low = middle + 1
    else high = middle
  }
  values.splice(low, 0, value)
}

function parentBarycenter(parentIDs: Set<string>, orderByID: Map<string, number>): number {
  let sum = 0
  let count = 0
  for (const id of parentIDs) {
    const order = orderByID.get(id)
    if (order === undefined) continue
    sum += order
    count++
  }
  return count === 0 ? Number.MAX_SAFE_INTEGER : sum / count
}

function compareIntents(left: Intent, right: Intent): number {
  const leftTime = left.created_at ?? ''
  const rightTime = right.created_at ?? ''
  if (leftTime !== rightTime) return leftTime.localeCompare(rightTime)
  return left.id.localeCompare(right.id, undefined, { numeric: true })
}

function compareIntentNodes(left: ExplorationGraphNode, right: ExplorationGraphNode): number {
  if (left.kind !== right.kind) return left.kind === 'origin' ? -1 : 1
  if (left.intent && right.intent) return compareIntents(left.intent, right.intent)
  return left.id.localeCompare(right.id)
}

function edgePath(parent: ExplorationGraphNode, child: ExplorationGraphNode): string {
  const startX = parent.x + parent.width
  const startY = parent.y + parent.height / 2
  const endX = child.x
  const endY = child.y + child.height / 2
  const bend = Math.max(36, (endX - startX) / 2)
  return `M ${startX} ${startY} C ${startX + bend} ${startY}, ${endX - bend} ${endY}, ${endX} ${endY}`
}

function groupBy<T, K extends string | number>(values: T[], key: (value: T) => K): Map<K, T[]> {
  const groups = new Map<K, T[]>()
  for (const value of values) {
    const groupKey = key(value)
    const group = groups.get(groupKey)
    if (group) group.push(value)
    else groups.set(groupKey, [value])
  }
  return groups
}
