import { screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { renderWithProviders } from '@/test/render'
import { StatusTag } from './StatusTag'

describe('StatusTag', () => {
  it('renders localized status text', () => {
    renderWithProviders(<StatusTag status="running" />)
    expect(screen.getByText('运行中')).toBeInTheDocument()
  })

  it('renders English status text', () => {
    renderWithProviders(<StatusTag status="succeeded" />, { locale: 'en' })
    expect(screen.getByText('Succeeded')).toBeInTheDocument()
  })

  it('puts the status class on the tag so the color variable resolves', () => {
    renderWithProviders(<StatusTag status="stagnated" />)
    const tag = document.querySelector('.status-tag')
    expect(tag).toHaveClass('status-stagnated')
  })
})
