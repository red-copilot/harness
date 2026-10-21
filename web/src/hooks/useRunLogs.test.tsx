import { renderHook, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { getLogs } from '@/api/client'
import { useRunLogs } from './useRunLogs'

vi.mock('@/api/client', () => ({
  getLogs: vi.fn(),
}))

describe('useRunLogs', () => {
  it('loads a tail and appends the next page', async () => {
    vi.mocked(getLogs)
      .mockResolvedValueOnce([
        { seq: 1, run_id: 'run_1', stream: 'stdout', time: '2026-10-20T12:00:00Z', text: 'one' },
      ])
      .mockResolvedValueOnce([
        { seq: 2, run_id: 'run_1', stream: 'stdout', time: '2026-10-20T12:00:01Z', text: 'two' },
      ])
    const { result } = renderHook(() => useRunLogs('run_1', false))
    await waitFor(() => expect(result.current.logs).toHaveLength(2))
    expect(result.current.logs.map((log) => log.text)).toEqual(['one', 'two'])
    expect(vi.mocked(getLogs)).toHaveBeenNthCalledWith(
      2,
      'run_1',
      expect.objectContaining({ afterSeq: 1, limit: 2000 }),
      expect.any(AbortSignal),
    )
  })
})
