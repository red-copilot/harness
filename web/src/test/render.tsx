import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { App as AntdApp, ConfigProvider } from 'antd'
import { render } from '@testing-library/react'
import type { ReactElement } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { I18nProvider } from '@/i18n/I18nContext'
import { ThemeProvider } from '@/theme/ThemeContext'

export function renderWithProviders(
  ui: ReactElement,
  { route = '/', locale = 'zh' }: { route?: string; locale?: 'zh' | 'en' } = {},
) {
  localStorage.setItem('harness.ui.locale', locale)
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: 0 },
      mutations: { retry: false },
    },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <I18nProvider>
        <ThemeProvider>
          <ConfigProvider>
            <AntdApp>
              <MemoryRouter initialEntries={[route]} future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>{ui}</MemoryRouter>
            </AntdApp>
          </ConfigProvider>
        </ThemeProvider>
      </I18nProvider>
    </QueryClientProvider>,
  )
}
