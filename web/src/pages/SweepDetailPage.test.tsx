import { screen } from '@testing-library/react'
import { Route, Routes } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import SweepDetailPage from './SweepDetailPage'
import { http, HttpResponse, renderWithProviders, server } from '@/test/renderTestUtils'

describe('SweepDetailPage', () => {
  it('renders sweep results and run links', async () => {
    server.use(
      http.get('*/v1/tsec/sweeps/sweep_1', () =>
        HttpResponse.json({
          id: 'sweep_1',
          status: 'succeeded',
          run_ids: ['run_1'],
          results: [
            {
              run_id: 'run_1',
              status: 'succeeded',
              verdict: { success: true, score: 2 },
              usage: { total_tokens: 8, cost_usd: 0.01 },
              started_at: '2026-10-20T12:00:00Z',
              finished_at: '2026-10-20T12:00:02Z',
            },
          ],
          created_at: '2026-10-20T12:00:00Z',
          finished_at: '2026-10-20T12:00:03Z',
        }),
      ),
    )
    renderWithProviders(
      <Routes>
        <Route path="/sweeps/:sweepId" element={<SweepDetailPage />} />
      </Routes>,
      { route: '/sweeps/sweep_1' },
    )
    expect(await screen.findByText('sweep_1')).toBeInTheDocument()
    expect((await screen.findAllByText('run_1')).length).toBeGreaterThan(0)
    expect((await screen.findAllByText('成功')).length).toBeGreaterThan(0)
  })
})
