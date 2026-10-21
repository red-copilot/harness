import '@testing-library/jest-dom/vitest'
import { cleanup, configure } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
import { afterAll, afterEach, beforeAll, beforeEach, vi } from 'vitest'

// jsdom renders under load on CI; the default 1s findBy* budget is too tight
// for full antd pages, so a slow machine looks like a real assertion failure.
configure({ asyncUtilTimeout: 10000 })

export const server = setupServer()

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))
beforeEach(() => localStorage.clear())
afterEach(() => {
  cleanup()
  server.resetHandlers()
})
afterAll(() => server.close())

Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  }),
})

const originalGetComputedStyle = window.getComputedStyle.bind(window)
window.getComputedStyle = (element: Element) => originalGetComputedStyle(element)

class ResizeObserverMock {
  observe() {}
  unobserve() {}
  disconnect() {}
}

Object.defineProperty(window, 'ResizeObserver', { writable: true, value: ResizeObserverMock })
Object.defineProperty(globalThis, 'ResizeObserver', { writable: true, value: ResizeObserverMock })
Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', { writable: true, value: vi.fn() })
Object.defineProperty(window, 'scrollTo', { writable: true, value: vi.fn() })
Object.defineProperty(URL, 'createObjectURL', { writable: true, value: vi.fn(() => 'blob:test') })
Object.defineProperty(URL, 'revokeObjectURL', { writable: true, value: vi.fn() })
Object.defineProperty(navigator, 'clipboard', {
  writable: true,
  value: { writeText: vi.fn().mockResolvedValue(undefined) },
})

export { http, HttpResponse }
