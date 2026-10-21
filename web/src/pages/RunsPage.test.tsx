import { fireEvent, screen, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import RunsPage from './RunsPage'
import { http, HttpResponse, renderWithProviders, server } from '@/test/renderTestUtils'

describe('RunsPage', () => {
  it('filters runs by search text', async () => {
    server.use(
      http.get('*/v1/runs', () =>
        HttpResponse.json([
          {
            id: 'run_alpha',
            status: 'succeeded',
            created_at: '2026-10-20T12:00:00Z',
            spec: { task: { id: 'alpha', objective: 'first' }, agents: [{ id: 'pi' }] },
          },
          {
            id: 'run_beta',
            status: 'failed',
            created_at: '2026-10-20T12:01:00Z',
            spec: { task: { id: 'beta', objective: 'second' }, agents: [{ id: 'pi' }] },
          },
        ]),
      ),
    )
    renderWithProviders(<RunsPage />)
    expect(await screen.findByText('run_alpha')).toBeInTheDocument()
    expect(await screen.findByText('run_beta')).toBeInTheDocument()
    fireEvent.change(screen.getByPlaceholderText('搜索运行 ID、任务、目标或 Agent'), { target: { value: 'beta' } })
    await waitFor(() => expect(screen.queryByText('run_alpha')).not.toBeInTheDocument())
    expect(screen.getByText('run_beta')).toBeInTheDocument()
  })
})
