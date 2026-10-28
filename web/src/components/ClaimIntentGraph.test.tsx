import { fireEvent, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { BoardResponse } from '@/api/types'
import { renderWithProviders } from '@/test/renderTestUtils'
import { ClaimIntentGraph } from './ClaimIntentGraph'

const graph: BoardResponse = {
  claims: [
    { id: 'origin', run_id: 'run-1', task_id: 'task', kind: 'origin', description: 'Target is authorized' },
    { id: 'goal', run_id: 'run-1', task_id: 'task', kind: 'goal', description: 'Goal must not become a node' },
    { id: 'claim-1', run_id: 'run-1', task_id: 'task', kind: 'finding', description: 'Found an exposed service', source_intent_id: 'intent-1' },
    { id: 'claim-2', run_id: 'run-1', task_id: 'task', kind: 'finding', description: 'Found an admin route', source_intent_id: 'intent-2' },
  ],
  intents: [
    { id: 'intent-1', run_id: 'run-1', kind: 'bootstrap', from_claim_ids: ['origin'], description: 'Probe the service', state: 'concluded', slot: 1, executor_id: 'executor-1', result_claim_id: 'claim-1' },
    { id: 'intent-2', run_id: 'run-1', kind: 'explore', from_claim_ids: ['claim-1'], description: 'Trace the admin route', state: 'claimed', slot: 1, executor_id: 'executor-1', result_claim_id: 'claim-2' },
  ],
  hints: [{ id: 'hint-1', run_id: 'run-1', content: 'Focus on the admin route', creator: 'operator' }],
  attempts: [
    { id: 'attempt-1', run_id: 'run-1', intent_id: 'intent-1', slot: 1, session_id: 'executor-1', phase: 'concluded' },
  ],
  observations: [
    { id: 'observation-1', run_id: 'run-1', intent_id: 'intent-1', attempt_id: 'attempt-1', kind: 'http.response', summary: 'Login page exposed', target: '10.0.0.1:80' },
  ],
  artifacts: [
    { id: 'artifact-1', run_id: 'run-1', attempt_id: 'attempt-1', name: 'login.html', media_type: 'text/html', size: 2048, sha256: 'abc123' },
  ],
  slots: [1],
  board_revision: 2,
  reason: { active: false, frontier_version: 2, free_slots: [1] },
}

describe('ClaimIntentGraph', () => {
  it('renders one origin and one left-to-right node per Intent', () => {
    renderWithProviders(<ClaimIntentGraph graph={graph} />)
    expect(screen.getByTestId('exploration-node-origin')).toBeInTheDocument()
    expect(screen.getByTestId('exploration-node-intent-1')).toBeInTheDocument()
    expect(screen.getByTestId('exploration-node-intent-2')).toBeInTheDocument()
    expect(screen.queryByText('Goal must not become a node')).not.toBeInTheDocument()
    expect(Number(screen.getByTestId('exploration-node-intent-1').getAttribute('data-node-level'))).toBeLessThan(
      Number(screen.getByTestId('exploration-node-intent-2').getAttribute('data-node-level')),
    )
  })

  it('renders dependency edges and dashed unfinished links', () => {
    renderWithProviders(<ClaimIntentGraph graph={graph} />)
    expect(screen.getByTestId('exploration-edge-__exploration_origin__->intent-1')).toHaveClass('exploration-graph-edge-root')
    expect(screen.getByTestId('exploration-edge-intent-1->intent-2')).toHaveClass('exploration-graph-edge-planned')
    expect(screen.getByTestId('exploration-node-intent-2')).toHaveClass('exploration-graph-node-active')
  })

  it('opens the node drawer with attempts, observations, artifacts, and hints', () => {
    renderWithProviders(<ClaimIntentGraph graph={graph} />)
    fireEvent.click(screen.getByTestId('exploration-node-intent-1'))
    let drawer = screen.getByRole('dialog')
    expect(within(drawer).getAllByText('Probe the service').length).toBeGreaterThan(0)
    expect(within(drawer).getByText('attempt-1')).toBeInTheDocument()
    expect(within(drawer).getByText('Login page exposed')).toBeInTheDocument()
    expect(within(drawer).getByText('login.html')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('exploration-node-origin'))
    drawer = screen.getByRole('dialog')
    expect(within(drawer).getByText('Focus on the admin route')).toBeInTheDocument()
  })

  it('handles nullable collections from older server responses', () => {
    const nullableGraph = {
      ...graph,
      intents: null,
      attempts: [],
      observations: null,
      artifacts: null,
      hints: null,
    } as unknown as BoardResponse
    renderWithProviders(<ClaimIntentGraph graph={nullableGraph} />)
    expect(screen.getByTestId('exploration-node-origin')).toBeInTheDocument()
    expect(screen.getByText('等待 Reasoner 提议新的 Intent…')).toBeInTheDocument()
  })
})
