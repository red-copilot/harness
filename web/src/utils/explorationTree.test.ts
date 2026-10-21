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

describe('extractBranchView v1 events', () => {
  it('handles review branches, retries, artifacts and knowledge versions', () => {
    const view = extractBranchView([
      event(1, 'exploration.branch.created', { id: 'root', run_id: 'run_1', task_id: 'task-1', kind: 'root', depth: 0, topic: 'root', status: 'open' }),
      event(2, 'exploration.branch.created', { id: 'review-1', parent_id: 'root', run_id: 'run_1', task_id: 'task-1', kind: 'review', depth: 1, topic: 'review.auth', status: 'open', review_of: ['k-1', 'k-2'] }),
      event(3, 'exploration.branch.updated', { id: 'review-1', run_id: 'run_1', task_id: 'task-1', kind: 'review', depth: 1, topic: 'review.auth', status: 'resolved', review_of: ['k-1', 'k-2'] }),
      event(4, 'exploration.attempt.failed', { id: 'attempt-1', run_id: 'run_1', branch_id: 'review-1', status: 'failed', outcome: 'failure', error: 'retry' }),
      event(5, 'exploration.attempt.completed', { id: 'attempt-2', run_id: 'run_1', branch_id: 'review-1', status: 'completed', outcome: 'local_success' }),
      event(6, 'artifact.stored', { id: 'sha256:abc', run_id: 'run_1', attempt_id: 'attempt-2', name: 'proof.txt', media_type: 'text/plain', size: 12, sha256: 'abc', redacted: true }),
      event(7, 'knowledge.promoted', { id: 'k-1', task_id: 'task-1', run_id: 'run_1', topic: 'web.auth', kind: 'adapter.anchor', statement: 'auth exists', trust: 'corroborated', reuse: 'stable', version: 2, contradicts: [] }),
    ])
    expect(view.branches.find((branch) => branch.id === 'review-1')).toMatchObject({ kind: 'review', status: 'resolved', reviewOf: ['k-1', 'k-2'] })
    expect(view.attempts).toHaveLength(2)
    expect(view.artifacts[0]).toMatchObject({ name: 'proof.txt', redacted: true })
    expect(view.knowledge[0]).toMatchObject({ id: 'k-1', trust: 'corroborated', version: 2 })
  })

  it('ignores unknown events without dropping the view', () => {
    const view = extractBranchView([
      event(1, 'exploration.branch.created', { id: 'root', run_id: 'run_1', task_id: 'task-1', kind: 'root', depth: 0, topic: 'root', status: 'open' }),
      event(2, 'future.unknown', { ignored: true }),
    ])
    expect(view.branches).toHaveLength(1)
  })
})
