import { screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { EventRecord } from '@/api/types'
import { renderWithProviders } from '@/test/renderTestUtils'
import { ExplorationTimeline } from './ExplorationTimeline'

function event(seq: number, type: EventRecord['type'], data: unknown): EventRecord {
  return { seq, run_id: 'run_1', session_id: '', source: 'control', type, time: `2026-10-21T08:00:0${seq}Z`, data }
}

describe('BranchTreeTimeline', () => {
  it('renders typed branch state and durable knowledge', () => {
    renderWithProviders(<ExplorationTimeline events={[
      event(1, 'exploration.branch.created', { branch: { id: 'root', task_id: 'task-1', depth: 0, topic: 'root', phase: 'closed', outcome: 'supported', scope: 'target-1' } }),
      event(2, 'exploration.branch.created', { branch: { id: 'branch-a', parent_id: 'root', task_id: 'task-1', depth: 1, topic: 'web.oa', goal: 'find OA', hypothesis: 'OA exists', phase: 'closed', outcome: 'supported', scope: 'target-1' } }),
      event(3, 'knowledge.promoted', { knowledge: { id: 'k-1', topic: 'web.oa', kind: 'anchor', statement: 'OA path exists', trust: 'observed', scope: 'target' } }),
    ]} />)
    expect(screen.getAllByText('web.oa').length).toBeGreaterThan(0)
    expect(screen.getByText('OA path exists')).toBeInTheDocument()
    expect(screen.getAllByText('supported').length).toBeGreaterThan(0)
    expect(screen.getByText('target')).toBeInTheDocument()
  })
})
