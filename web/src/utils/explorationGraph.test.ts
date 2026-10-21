import { describe, expect, it } from 'vitest'
import { buildExplorationGraph, factNodeId, ORIGIN_NODE_ID } from './explorationGraph'
import type { HypothesisView } from './exploration'

function view(overrides: Partial<HypothesisView> = {}): HypothesisView {
  return {
    attempts: [],
    brief: {},
    frontiers: [],
    facts: [],
    activity: {},
    current: [],
    ...overrides,
  }
}

describe('buildExplorationGraph', () => {
  it('starts with a single origin node before any frontier exists', () => {
    const graph = buildExplorationGraph(view({ objective: 'find the flag' }))
    expect(graph.nodes).toHaveLength(1)
    expect(graph.nodes[0]).toMatchObject({ id: ORIGIN_NODE_ID, kind: 'origin', depth: 0 })
    expect(graph.edges).toEqual([])
  })

  it('places frontiers to the right of the origin and facts further right', () => {
    const graph = buildExplorationGraph(
      view({
        frontiers: [
          {
            id: 'frontier-a',
            question: '根路径是否返回 HTTP 200？',
            targetHint: 'web',
            probeKey: 'http.get',
            params: ['/'],
            expectedEvidence: '200',
            failureEvidence: '404',
            status: 'done',
            round: 1,
            anchors: [],
            seq: 2,
          },
        ],
        facts: [
          {
            id: 'f-1-1',
            claim: '根路径返回 HTTP 200',
            evidence: 'curl -i 输出 HTTP/1.1 200 OK',
            trust: 'observed',
            contested: false,
            targets: ['web'],
            sourceItemId: 'frontier-a',
            round: 1,
            seq: 3,
          },
        ],
      }),
    )
    const origin = graph.nodes.find((node) => node.id === ORIGIN_NODE_ID)!
    const frontier = graph.nodes.find((node) => node.id === 'frontier-a')!
    const fact = graph.nodes.find((node) => node.id === factNodeId('f-1-1'))!
    expect(frontier.x).toBeGreaterThan(origin.x + origin.width)
    expect(fact.x).toBeGreaterThan(frontier.x + frontier.width)
    expect(graph.width).toBeGreaterThan(fact.x + fact.width)
    expect(graph.edges.map((edge) => edge.id)).toEqual([
      `${ORIGIN_NODE_ID}->frontier-a`,
      `frontier-a->${factNodeId('f-1-1')}`,
    ])
  })

  it('links a derived frontier from its parent and anchors', () => {
    const graph = buildExplorationGraph(
      view({
        frontiers: [
          {
            id: 'frontier-a',
            question: '第一轮',
            targetHint: 'web',
            probeKey: 'http.get',
            params: ['/'],
            expectedEvidence: '200',
            failureEvidence: '404',
            status: 'done',
            round: 1,
            anchors: [],
            seq: 2,
          },
          {
            id: 'frontier-b',
            question: '第二轮',
            targetHint: 'web',
            probeKey: 'http.get',
            params: ['/admin'],
            expectedEvidence: 'form',
            failureEvidence: '404',
            status: 'open',
            round: 2,
            parentId: 'frontier-a',
            anchors: [],
            seq: 5,
          },
        ],
      }),
    )
    expect(graph.edges.some((edge) => edge.id === 'frontier-a->frontier-b' && edge.tone === 'derived')).toBe(true)
    const parent = graph.nodes.find((node) => node.id === 'frontier-a')!
    const child = graph.nodes.find((node) => node.id === 'frontier-b')!
    expect(child.depth).toBeGreaterThan(parent.depth)
  })

  it('never overlaps nodes inside one column', () => {
    const frontiers = Array.from({ length: 3 }, (_, index) => ({
      id: `frontier-${index}`,
      question: `假设 ${index}`,
      targetHint: 'web',
      probeKey: 'http.get',
      params: ['/'],
      expectedEvidence: 'e',
      failureEvidence: 'f',
      status: 'open',
      round: 1,
      anchors: [],
      seq: index,
    }))
    const graph = buildExplorationGraph(view({ frontiers }))
    const column = graph.nodes.filter((node) => node.depth === 1).sort((a, b) => a.y - b.y)
    expect(column).toHaveLength(3)
    for (let index = 1; index < column.length; index += 1) {
      expect(column[index].y).toBeGreaterThanOrEqual(column[index - 1].y + column[index - 1].height)
    }
  })
})
