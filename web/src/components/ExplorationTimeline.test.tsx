import { screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { EventRecord } from '@/api/types'
import { renderWithProviders } from '@/test/renderTestUtils'
import { ExplorationTimeline } from './ExplorationTimeline'

function event(seq: number, type: string, data: unknown): EventRecord {
  return {
    seq,
    run_id: 'run_1',
    session_id: '',
    source: 'control',
    type,
    time: `2026-10-21T07:00:0${seq}Z`,
    data,
  }
}

describe('ExplorationTimeline', () => {
  it('shows the initialized blackboard and open frontier before a report exists', () => {
    renderWithProviders(
      <ExplorationTimeline
        events={[
          event(1, 'exploration.initialized', {
            phase: 'initialized',
            objective: 'find the flag',
            targets: [{ id: 'web', addresses: ['10.0.0.5:80'] }],
            progress: { progress: 0, total: 1 },
            facts: [],
            frontiers: [],
            policy: { rounds: 6, max_workers: 1 },
          }),
          event(2, 'exploration.frontiers.created', {
            round: 1,
            items: [{
              id: 'frontier-a',
              question: 'Inspect the HTTP root',
              target_hint: 'web',
              expected_evidence: 'status and headers',
              status: 'open',
              round: 1,
              fingerprint: 'abc',
            }],
          }),
          event(3, 'exploration.batch.ready', {
            round: 1,
            max_workers: 1,
          }),
        ]}
      />,
    )

    expect(screen.getByText('find the flag')).toBeInTheDocument()
    expect(screen.getByText('web · 10.0.0.5:80')).toBeInTheDocument()
    expect(screen.getByText('Inspect the HTTP root')).toBeInTheDocument()
    expect(screen.getByText('status and headers')).toBeInTheDocument()
  })
})
