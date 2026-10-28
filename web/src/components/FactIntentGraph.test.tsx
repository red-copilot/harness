import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { GraphResponse } from '@/api/types'
import { FactIntentGraph } from './FactIntentGraph'

const graph: GraphResponse = {
  facts: [
    { id: 'origin', run_id: 'run-1', task_id: 'task', kind: 'origin', description: 'Target is authorized' },
    { id: 'fact-1', run_id: 'run-1', task_id: 'task', kind: 'finding', description: 'Found an exposed service', source_intent_id: 'intent-1' },
  ],
  intents: [
    { id: 'intent-1', run_id: 'run-1', kind: 'explore', from_fact_ids: ['origin'], description: 'Probe the service', state: 'claimed', slot: 1, executor_id: 'executor-1' },
  ],
  hints: [{ id: 'hint-1', run_id: 'run-1', content: 'Focus on web', creator: 'operator' }],
  attempts: [{ id: 'attempt-1', run_id: 'run-1', intent_id: 'intent-1', slot: 1, session_id: 'executor-1', phase: 'running' }],
  slots: [2],
  board_revision: 1,
  reason: { active: false, frontier_version: 1 },
}

describe('FactIntentGraph', () => {
  it('renders facts, intents, hints, and both slots', () => {
    render(<FactIntentGraph graph={graph} />)
    expect(screen.getByText('Found an exposed service')).toBeInTheDocument()
    expect(screen.getAllByText('Probe the service').length).toBeGreaterThan(0)
    expect(screen.getByText('Focus on web')).toBeInTheDocument()
    expect(screen.getAllByText('slot-1').length).toBeGreaterThan(0)
    expect(screen.getByText('slot-2')).toBeInTheDocument()
  })
})
