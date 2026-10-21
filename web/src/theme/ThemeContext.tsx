/* eslint-disable react-refresh/only-export-components -- context hooks share this module */
import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'

export type ThemeMode = 'light' | 'dark'

const STORAGE_KEY = 'harness.ui.theme'

interface ThemeValue {
  mode: ThemeMode
  toggleTheme: () => void
}

const ThemeContext = createContext<ThemeValue | null>(null)

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [mode, setMode] = useState<ThemeMode>(initialTheme)
  useEffect(() => {
    document.documentElement.dataset.theme = mode
    document.documentElement.style.colorScheme = mode
    try {
      localStorage.setItem(STORAGE_KEY, mode)
    } catch {
      // A blocked localStorage should not break rendering.
    }
  }, [mode])
  const toggleTheme = useCallback(
    () => setMode((current) => (current === 'dark' ? 'light' : 'dark')),
    [],
  )
  const value = useMemo(() => ({ mode, toggleTheme }), [mode, toggleTheme])
  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>
}

export function useTheme(): ThemeValue {
  const value = useContext(ThemeContext)
  if (!value) throw new Error('useTheme must be used inside ThemeProvider')
  return value
}

function initialTheme(): ThemeMode {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (stored === 'light' || stored === 'dark') return stored
  } catch {
    // Fall through to system preference.
  }
  return window.matchMedia?.('(prefers-color-scheme: dark)')?.matches ? 'dark' : 'light'
}
