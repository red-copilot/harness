/* eslint-disable react-refresh/only-export-components -- context hooks share this module */
import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { messages } from './messages'
import type { Locale, MessageKey, MessageVars } from './messages'

const STORAGE_KEY = 'harness.ui.locale'

interface I18nValue {
  locale: Locale
  setLocale: (locale: Locale) => void
  t: (key: MessageKey, vars?: MessageVars) => string
}

const I18nContext = createContext<I18nValue | null>(null)

export function I18nProvider({ children }: { children: ReactNode }) {
  const [locale, setLocaleState] = useState<Locale>(initialLocale)
  useEffect(() => {
    document.documentElement.lang = locale === 'zh' ? 'zh-CN' : 'en'
    try {
      localStorage.setItem(STORAGE_KEY, locale)
    } catch {
      // A blocked localStorage should not break rendering.
    }
  }, [locale])
  const setLocale = useCallback((next: Locale) => setLocaleState(next), [])
  const t = useCallback(
    (key: MessageKey, vars?: MessageVars) => interpolate(messages[locale][key], vars),
    [locale],
  )
  const value = useMemo(() => ({ locale, setLocale, t }), [locale, setLocale, t])
  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>
}

export function useI18n(): I18nValue {
  const value = useContext(I18nContext)
  if (!value) throw new Error('useI18n must be used inside I18nProvider')
  return value
}

function initialLocale(): Locale {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (stored === 'zh' || stored === 'en') return stored
  } catch {
    // Fall through to browser detection.
  }
  return navigator.language.toLowerCase().startsWith('zh') ? 'zh' : 'en'
}

function interpolate(template: string, vars?: MessageVars): string {
  if (!vars) return template
  return template.replace(/\{(\w+)\}/g, (_, key: string) => String(vars[key] ?? `{${key}}`))
}
