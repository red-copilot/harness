import { describe, expect, it } from 'vitest'
import type { EventRecord } from '@/api/types'
import { extractExplorationCycles, extractHypothesisView } from './exploration'

function event(seq: number, type: string, sessionId: string, data: unknown): EventRecord {
  return {
    seq,
    run_id: 'run_1',
    session_id: sessionId,
    source: type.startsWith('exploration.') ? 'control' : 'agent',
    type,
    time: `2026-10-21T04:00:0${seq}Z`,
    data,
  }
}

describe('extractExplorationCycles', () => {
  it('pairs turn, state and frontier events per session and returns newest first', () => {
    const events = [
      event(1, 'turn_end', 'pi-a', { message: { content: [] }, toolResults: [] }),
      event(2, 'turn_end', 'pi-b', { message: { content: [] }, toolResults: [] }),
      event(3, 'exploration.state.updated', 'pi-a', { revision: 1, attempts: [{}], observations: [] }),
      event(4, 'exploration.state.updated', 'pi-b', { revision: 4, attempts: [{}, {}], observations: [] }),
      event(5, 'exploration.frontier.updated', 'pi-a', {
        state_revision: 1,
        frontier: { items: [{ id: 'q1', kind: 'question', text: 'verify a', status: 'open', priority: 90 }] },
        focus: { id: 'q1', kind: 'question', text: 'verify a', status: 'open', priority: 90 },
      }),
      event(6, 'exploration.frontier.updated', 'pi-b', {
        state_revision: 4,
        frontier: { items: [] },
      }),
    ]

    const cycles = extractExplorationCycles(events)
    expect(cycles).toHaveLength(2)
    expect(cycles[0].turn.session_id).toBe('pi-b')
    expect(cycles[0].state?.revision).toBe(4)
    expect(cycles[1].turn.session_id).toBe('pi-a')
    expect(cycles[1].frontier?.focus?.text).toBe('verify a')
  })

  it('ignores incomplete or malformed exploration events', () => {
    const cycles = extractExplorationCycles([
      event(1, 'exploration.state.updated', 'pi', { revision: 'bad' }),
      event(2, 'turn_end', 'pi', {}),
      event(3, 'exploration.frontier.updated', 'pi', null),
    ])
    expect(cycles).toHaveLength(1)
    expect(cycles[0].turn.seq).toBe(2)
    expect(cycles[0].state).toBeUndefined()
    expect(cycles[0].frontier).toBeUndefined()
  })
})

describe('extractHypothesisView', () => {
  it('merges frontier assignment, report, facts, failures and backend progress', () => {
    const view = extractHypothesisView([
      event(10, 'backend.progress', '', { progress: 2, total: 6 }),
      event(1, 'exploration.item.assigned', '', {
        round: 1,
        item_id: 'frontier-a',
        executor_id: 'executor-1',
      }),
      event(2, 'exploration.report.received', '', {
        round: 1,
        item_id: 'frontier-a',
        verdict: 'found',
      }),
      event(3, 'exploration.facts.merged', '', {
        round: 1,
        item_id: 'frontier-a',
        added: ['f-1'],
        corroborated: [],
        contested: [],
      }),
      event(4, 'exploration.item.assigned', '', {
        round: 1,
        item_id: 'frontier-b',
        executor_id: 'executor-2',
      }),
      event(5, 'exploration.item.failed', '', {
        round: 1,
        item_id: 'frontier-b',
        error: 'report item_id mismatch',
      }),
    ])

    expect(view.progress).toEqual({ current: 2, total: 6 })
    expect(view.attempts).toHaveLength(2)
    const completed = view.attempts.find((attempt) => attempt.itemId === 'frontier-a')
    expect(completed).toMatchObject({
      round: 1,
      executorId: 'executor-1',
      verdict: 'found',
      status: 'done',
      facts: { added: ['f-1'], corroborated: [], contested: [] },
    })
    const failed = view.attempts.find((attempt) => attempt.itemId === 'frontier-b')
    expect(failed).toMatchObject({ status: 'failed', error: 'report item_id mismatch' })
  })

  it('returns an empty view for malformed hypothesis events', () => {
    const view = extractHypothesisView([
      event(1, 'exploration.item.assigned', '', null),
      event(2, 'exploration.report.received', '', { item_id: 42 }),
      event(3, 'backend.progress', '', { progress: 'bad', total: 6 }),
    ])
    expect(view.attempts).toEqual([])
    expect(view.progress).toBeUndefined()
  })
})
