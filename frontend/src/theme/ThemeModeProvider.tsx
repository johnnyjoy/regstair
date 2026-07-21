import { CssBaseline, ThemeProvider } from '@mui/material'
import { createContext, useContext, useEffect, useMemo, useState, type PropsWithChildren } from 'react'

import { createRegstairTheme } from './regstairTheme'
import { isThemePreference, resolveThemeMode, type ThemePreference } from './themeMode'

const storageKey = 'regstair-theme'

type ThemeModeContextValue = {
  preference: ThemePreference
  setPreference: (preference: ThemePreference) => void
}

const ThemeModeContext = createContext<ThemeModeContextValue | null>(null)

export function ThemeModeProvider({ children }: PropsWithChildren) {
  const [preference, setPreferenceState] = useState<ThemePreference>(() => {
    const stored = window.localStorage.getItem(storageKey)
    return isThemePreference(stored) ? stored : 'system'
  })
  const [systemDark, setSystemDark] = useState(() => window.matchMedia('(prefers-color-scheme: dark)').matches)

  useEffect(() => {
    const media = window.matchMedia('(prefers-color-scheme: dark)')
    const update = () => setSystemDark(media.matches)
    media.addEventListener('change', update)
    return () => media.removeEventListener('change', update)
  }, [])

  const setPreference = (next: ThemePreference) => {
    window.localStorage.setItem(storageKey, next)
    setPreferenceState(next)
  }
  const theme = useMemo(() => createRegstairTheme(resolveThemeMode(preference, systemDark)), [preference, systemDark])

  return (
    <ThemeModeContext.Provider value={{ preference, setPreference }}>
      <ThemeProvider theme={theme}>
        <CssBaseline />
        {children}
      </ThemeProvider>
    </ThemeModeContext.Provider>
  )
}

export function useThemeMode() {
  const value = useContext(ThemeModeContext)
  if (!value) throw new Error('useThemeMode must be used within ThemeModeProvider')
  return value
}
