import { screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { EventRecord } from '@/api/types'
import { renderWithProviders } from '@/test/renderTestUtils'
import { ExplorationTimeline } from './ExplorationTimeline'

function event(seq: number, type: EventRecord['type'], data: unknown): EventRecord {
  return { seq, run_id: 'run_1', session_id: '', source: 'harness', type, time: `2026-10-21T07:00:0${seq}Z`, data }
}

describe('ExplorationTimeline', () => {
  it('renders only the canonical branch projection', () => {
    renderWithProviders(<ExplorationTimeline events={[
      event(1, 'exploration.branch.created', { branch: { id: 'root', depth: 0, topic: 'root', phase: 'open', outcome: 'pending', scope: 'target-1' } }),
      event(2, 'exploration.branch.created', { branch: { id: 'branch-a', parent_id: 'root', depth: 1, topic: 'web.auth', hypothesis: 'login exists', phase: 'closed', outcome: 'supported', scope: 'target-1' } }),
      event(3, 'exploration.attempt.completed', { attempt: { id: 'attempt-1', branch_id: 'branch-a', phase: 'completed', outcome: 'supported' } }),
    ]} />)
    expect(screen.getByTestId('branch-tree-timeline')).toBeInTheDocument()
    expect(screen.getAllByText('web.auth').length).toBeGreaterThan(0)
    expect(screen.getByText('login exists')).toBeInTheDocument()
  })

  it('degrades unknown and malformed events to the empty state', () => {
    renderWithProviders(<ExplorationTimeline events={[
      event(1, 'exploration.branch.created', { malformed: true }),
      { ...event(2, 'run.created', {}), type: 'future.unknown' },
    ]} />)
    expect(screen.getByText('还没有探索控制事件。')).toBeInTheDocument()
  })
})
