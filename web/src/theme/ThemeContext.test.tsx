import { fireEvent, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { renderWithProviders } from '@/test/render'
import { useTheme } from './ThemeContext'

function Probe() {
  const { mode, toggleTheme } = useTheme()
  return (
    <div>
      <span>{mode}</span>
      <button onClick={toggleTheme}>toggle</button>
    </div>
  )
}

describe('ThemeProvider', () => {
  it('toggles theme, sets the document attribute, and persists it', () => {
    renderWithProviders(<Probe />)
    expect(screen.getByText('light')).toBeInTheDocument()
    fireEvent.click(screen.getByText('toggle'))
    expect(screen.getByText('dark')).toBeInTheDocument()
    expect(document.documentElement.dataset.theme).toBe('dark')
    expect(localStorage.getItem('harness.ui.theme')).toBe('dark')
  })
})
