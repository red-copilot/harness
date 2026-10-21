import { describe, expect, it } from 'vitest'
import type { EventRecord } from '@/api/types'
import { extractBranchView } from './explorationTree'

function event(seq: number, type: string, data: unknown): EventRecord {
  return {
    seq,
    run_id: 'run_1',
    session_id: '',
    source: 'control',
    type,
    time: `2026-10-21T04:00:0${seq}Z`,
    data,
  }
}

describe('extractBranchView', () => {
  it('builds the branch tree from v2 events', () => {
    const view = extractBranchView([
      event(1, 'exploration.branch.created', {
        id: 'root', task_id: 'task-1', depth: 0, topic: 'root', status: 'resolved', scope: 'b-02',
      }),
      event(2, 'exploration.branch.created', {
        id: 'branch-a', parent_id: 'root', task_id: 'task-1', depth: 1, topic: 'web.oa',
        goal: 'find OA', hypothesis: 'OA is exposed', status: 'resolved', scope: 'b-02',
      }),
      event(3, 'exploration.attempt.completed', {
        id: 'attempt-1', branch_id: 'branch-a', session_id: 'executor-1', status: 'completed', outcome: 'local_success',
      }),
      event(4, 'exploration.observation.recorded', {
        id: 'obs-1', branch_id: 'branch-a', attempt_id: 'attempt-1', summary: 'OA path exists',
      }),
      event(5, 'exploration.knowledge.promoted', {
        id: 'k-1', task_id: 'task-1', topic: 'web.oa', kind: 'anchor', statement: 'OA path exists',
        trust: 'observed', reuse: 'stable',
      }),
      event(6, 'backend.progress', { progress: 2, total: 6 }),
    ])
    expect(view.branches).toHaveLength(2)
    expect(view.branches[1]).toMatchObject({ id: 'branch-a', parentId: 'root', depth: 1, topic: 'web.oa' })
    expect(view.attempts[0]).toMatchObject({ branchId: 'branch-a', outcome: 'local_success' })
    expect(view.observations[0].summary).toBe('OA path exists')
    expect(view.knowledge[0]).toMatchObject({ topic: 'web.oa', trust: 'observed' })
    expect(view.progress).toEqual({ current: 2, total: 6 })
  })

  it('ignores malformed and legacy events', () => {
    const view = extractBranchView([
      event(1, 'exploration.branch.created', { id: '', topic: 'bad' }),
      event(2, 'exploration.facts.merged', { item_id: 'old' }),
    ])
    expect(view.branches).toHaveLength(0)
    expect(view.attempts).toHaveLength(0)
  })
})
