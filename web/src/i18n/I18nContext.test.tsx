import { fireEvent, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { renderWithProviders } from '@/test/render'
import { useI18n } from './I18nContext'

function Probe() {
  const { locale, setLocale, t } = useI18n()
  return (
    <div>
      <span>{locale}</span>
      <span>{t('nav.runs')}</span>
      <button onClick={() => setLocale('en')}>switch</button>
    </div>
  )
}

describe('I18nProvider', () => {
  it('switches language and persists it', () => {
    renderWithProviders(<Probe />)
    expect(screen.getByText('运行')).toBeInTheDocument()
    fireEvent.click(screen.getByText('switch'))
    expect(screen.getByText('Runs')).toBeInTheDocument()
    expect(localStorage.getItem('harness.ui.locale')).toBe('en')
  })
})
