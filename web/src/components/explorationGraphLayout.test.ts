import { describe, expect, it } from 'vitest'
import type { BoardResponse } from '@/api/types'
import { layoutExplorationGraph } from './explorationGraphLayout'

const board: BoardResponse = {
  claims: [
    { id: 'origin', run_id: 'run-1', task_id: 'task', kind: 'origin', description: 'Initial task' },
    { id: 'goal', run_id: 'run-1', task_id: 'task', kind: 'goal', description: 'Goal' },
    { id: 'claim-1a', run_id: 'run-1', task_id: 'task', kind: 'finding', description: 'First result', source_intent_id: 'intent-1' },
    { id: 'claim-1b', run_id: 'run-1', task_id: 'task', kind: 'negative', description: 'Second result', source_intent_id: 'intent-1' },
    { id: 'claim-2', run_id: 'run-1', task_id: 'task', kind: 'finding', description: 'Second branch', source_intent_id: 'intent-2' },
    { id: 'claim-3', run_id: 'run-1', task_id: 'task', kind: 'finding', description: 'Merged result', source_intent_id: 'intent-3' },
  ],
  intents: [
    { id: 'intent-1', run_id: 'run-1', kind: 'bootstrap', from_claim_ids: ['origin'], description: 'Bootstrap', state: 'concluded', result_claim_id: 'claim-1a' },
    { id: 'intent-2', run_id: 'run-1', kind: 'explore', from_claim_ids: ['claim-1a', 'claim-1b'], description: 'Branch', state: 'claimed', result_claim_id: 'claim-2' },
    { id: 'intent-3', run_id: 'run-1', kind: 'explore', from_claim_ids: ['claim-1a', 'claim-2'], description: 'Merge', state: 'concluded', result_claim_id: 'claim-3' },
  ],
  hints: [],
  attempts: [
    { id: 'attempt-1', run_id: 'run-1', intent_id: 'intent-1', slot: 1, session_id: 'executor', phase: 'concluded' },
    { id: 'attempt-2', run_id: 'run-1', intent_id: 'intent-2', slot: 1, session_id: 'executor', phase: 'running' },
  ],
  observations: [
    { id: 'observation-1', run_id: 'run-1', intent_id: 'intent-1', attempt_id: 'attempt-1', kind: 'test', summary: 'Observed' },
  ],
  artifacts: [
    { id: 'artifact-1', run_id: 'run-1', attempt_id: 'attempt-1', name: 'evidence.txt', media_type: 'text/plain', size: 12, sha256: 'abc' },
  ],
  slots: [2],
  board_revision: 3,
  reason: { active: false, frontier_version: 3, free_slots: [1] },
}

describe('layoutExplorationGraph', () => {
  it('creates one origin node and excludes the goal claim', () => {
    const layout = layoutExplorationGraph(board)
    expect(layout.nodes.filter((node) => node.kind === 'origin')).toHaveLength(1)
    expect(layout.nodes.some((node) => node.id === 'goal')).toBe(false)
    expect(layout.nodes).toHaveLength(4)
  })

  it('uses multi-parent dependencies for left-to-right levels and edges', () => {
    const layout = layoutExplorationGraph(board)
    const node = new Map(layout.nodes.map((item) => [item.id, item]))
    expect(node.get('intent-1')?.level).toBe(1)
    expect(node.get('intent-2')?.level).toBe(2)
    expect(node.get('intent-3')?.level).toBe(3)
    expect(node.get('intent-1')!.x).toBeLessThan(node.get('intent-2')!.x)
    expect(node.get('intent-2')!.x).toBeLessThan(node.get('intent-3')!.x)
    const incoming = layout.edges.filter((edge) => edge.to === 'intent-3').map((edge) => edge.from)
    expect(incoming).toEqual(['intent-1', 'intent-2'])
  })

  it('deduplicates multiple source claims produced by the same intent', () => {
    const layout = layoutExplorationGraph(board)
    expect(layout.edges.filter((edge) => edge.from === 'intent-1' && edge.to === 'intent-2')).toHaveLength(1)
  })

  it('keeps unresolved intents visible without inventing edges', () => {
    const orphan = {
      ...board,
      intents: [{ id: 'orphan', run_id: 'run-1', kind: 'explore', from_claim_ids: ['missing'], description: 'Orphan', state: 'open' }],
      attempts: [],
      observations: [],
      artifacts: [],
    } satisfies BoardResponse
    const layout = layoutExplorationGraph(orphan)
    const node = layout.nodes.find((item) => item.id === 'orphan')
    expect(node?.level).toBe(1)
    expect(node?.unresolvedFrom).toEqual(['missing'])
    expect(layout.edges).toHaveLength(0)
  })

  it('sorts siblings deterministically by creation time and ID', () => {
    const siblings = {
      ...board,
      intents: [
        { id: 'intent-1', run_id: 'run-1', kind: 'bootstrap', from_claim_ids: ['origin'], description: 'Root', state: 'concluded', result_claim_id: 'claim-1a', created_at: '2026-01-01T00:00:00Z' },
        { id: 'intent-late', run_id: 'run-1', kind: 'explore', from_claim_ids: ['claim-1a'], description: 'Late sibling', state: 'open', created_at: '2026-01-01T00:00:02Z' },
        { id: 'intent-early', run_id: 'run-1', kind: 'explore', from_claim_ids: ['claim-1a'], description: 'Early sibling', state: 'open', created_at: '2026-01-01T00:00:01Z' },
      ],
      attempts: [],
      observations: [],
      artifacts: [],
    } satisfies BoardResponse
    const layout = layoutExplorationGraph(siblings)
    const sameLevel = layout.nodes.filter((node) => node.level === 2).sort((left, right) => left.order - right.order)
    expect(sameLevel.map((node) => node.id)).toEqual(['intent-early', 'intent-late'])
  })

  it('provides an initial node when the origin claim is missing', () => {
    const withoutOrigin = {
      ...board,
      claims: board.claims.filter((claim) => claim.kind !== 'origin'),
      intents: [],
      attempts: [],
      observations: [],
      artifacts: [],
    } satisfies BoardResponse
    const layout = layoutExplorationGraph(withoutOrigin)
    expect(layout.nodes).toHaveLength(1)
    expect(layout.nodes[0].kind).toBe('origin')
    expect(layout.nodes[0].origin).toBeUndefined()
  })

  it('associates attempts, observations, and artifacts with their intent', () => {
    const layout = layoutExplorationGraph(board)
    const first = layout.nodes.find((node) => node.id === 'intent-1')
    expect(first?.attempts).toHaveLength(1)
    expect(first?.observations).toHaveLength(1)
    expect(first?.artifacts).toHaveLength(1)
  })
})
