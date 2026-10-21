import { expect, test } from '@playwright/test'
import type { Page } from '@playwright/test'

const overview = {
  generated_at: '2026-10-21T09:00:00Z',
  window: 100,
  runs: {
    total: 2,
    active: 1,
    terminal: 1,
    queued: 0,
    provisioning: 0,
    running: 1,
    finalizing: 0,
    verifying: 0,
    succeeded: 1,
    failed: 0,
    cancelled: 0,
    timed_out: 0,
    interrupted: 0,
    stagnated: 0,
    success_rate: 0.5,
  },
  usage: { input_tokens: 1200, output_tokens: 300, total_tokens: 1500, cost_usd: 0.02 },
  duration: { samples: 1, average_ms: 1500, p50_ms: 1500, p95_ms: 1500 },
}

const runs = [
  {
    id: 'run_mobile',
    status: 'succeeded',
    created_at: '2026-10-21T09:00:00Z',
    finished_at: '2026-10-21T09:01:00Z',
    spec: { task: { id: 'mobile-check', objective: 'Verify the responsive console' }, planner: { id: 'pi' }, executors: [] },
  },
  {
    id: 'run_running',
    status: 'running',
    created_at: '2026-10-21T09:03:00Z',
    spec: { task: { id: 'live-check', objective: 'Inspect a live run' }, planner: { id: 'pi' }, executors: [] },
  },
]

async function mockApi(page: Page) {
  await page.addInitScript(() => {
    localStorage.setItem('harness.ui.locale', 'zh')
  })
  await page.route('**/healthz', (route) => route.fulfill({ json: { status: 'ok' } }))
  await page.route('**/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname
    if (path === '/v1/overview') return route.fulfill({ json: overview })
    if (path === '/v1/runs') return route.fulfill({ json: runs })
    if (path === '/v1/runs/run_mobile') return route.fulfill({ json: runs[0] })
    if (path === '/v1/runs/run_mobile/events') {
      return route.fulfill({ status: 200, contentType: 'text/event-stream', body: '' })
    }
    if (path === '/v1/runs/run_mobile/verdict') {
      return route.fulfill({ status: 404, json: { error: 'verdict not found' } })
    }
    if (path === '/v1/runs/run_mobile/logs') {
      return route.fulfill({
        json: [
          {
            seq: 1,
            run_id: 'run_mobile',
            session_id: 'executor-1',
            stream: 'stdout',
            time: '2026-10-21T09:00:30Z',
            text: 'first mobile log line that is long enough to verify wrapping behavior',
          },
        ],
      })
    }
    return route.fulfill({ status: 404, json: { error: 'not found' } })
  })
}

test.describe('responsive console', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('opens navigation as a drawer on mobile and closes it after navigation', async ({ page }) => {
    await mockApi(page)
    await page.goto('/')

    await expect(page.getByRole('heading', { name: '运行总览' })).toBeVisible()
    const menuButton = page.getByRole('button', { name: '打开导航' })
    await expect(menuButton).toBeVisible()
    await menuButton.click()

    const drawer = page.getByRole('dialog')
    await expect(drawer.getByRole('menuitem', { name: '运行' })).toBeVisible()
    await drawer.getByRole('menuitem', { name: '运行' }).click()

    await expect(page).toHaveURL(/\/runs$/)
    await expect(drawer).toBeHidden()
    await expect(page.getByRole('heading', { name: '运行' })).toBeVisible()
    const horizontalOverflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    )
    expect(horizontalOverflow).toBeLessThanOrEqual(1)
  })

  test('uses roomy wrapped rows for mobile logs', async ({ page }) => {
    await mockApi(page)
    await page.goto('/runs/run_mobile?tab=logs')

    await expect(page.getByText('first mobile log line that is long enough to verify wrapping behavior')).toBeVisible()
    const row = page.locator('.log-line').first()
    const box = await row.boundingBox()
    expect(box?.height ?? 0).toBeGreaterThanOrEqual(56)
  })
})
