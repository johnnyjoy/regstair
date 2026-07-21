import { createTheme, type ThemeOptions } from '@mui/material/styles'

import type { ResolvedThemeMode } from './themeMode'

const shared: ThemeOptions = {
  shape: { borderRadius: 6 },
  typography: {
    fontFamily: 'Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif',
    fontSize: 14,
    h1: { fontSize: '1.75rem', fontWeight: 720, lineHeight: 1.2 },
    h2: { fontSize: '1.125rem', fontWeight: 700, lineHeight: 1.3 },
    button: { fontWeight: 680, textTransform: 'none' },
  },
  components: {
    MuiButtonBase: { defaultProps: { disableRipple: true } },
    MuiButton: { defaultProps: { disableElevation: true } },
    MuiTooltip: { defaultProps: { arrow: true } },
    MuiPaper: { styleOverrides: { root: { backgroundImage: 'none' } } },
    MuiChip: { styleOverrides: { root: { fontWeight: 650 } } },
    MuiTableCell: { styleOverrides: { head: { fontSize: 12, fontWeight: 750, textTransform: 'uppercase' } } },
  },
}

export function createRegstairTheme(mode: ResolvedThemeMode) {
  const dark = mode === 'dark'
  return createTheme({
    ...shared,
    palette: {
      mode,
      primary: { main: dark ? '#57c7e8' : '#087e9f' },
      success: { main: dark ? '#4fd19b' : '#147d57' },
      warning: { main: dark ? '#f4bf5f' : '#9a6500' },
      error: { main: dark ? '#ff7b75' : '#c73732' },
      background: {
        default: dark ? '#0b1117' : '#f4f7f9',
        paper: dark ? '#111a22' : '#ffffff',
      },
      text: {
        primary: dark ? '#edf4f7' : '#13232c',
        secondary: dark ? '#9fb1bc' : '#526772',
      },
      divider: dark ? '#2a3a45' : '#d6e0e5',
    },
    components: {
      ...shared.components,
      MuiCssBaseline: {
        styleOverrides: {
          ':root': { colorScheme: mode },
          body: { letterSpacing: 0 },
          code: {
            fontFamily: '"IBM Plex Mono", "SFMono-Regular", Consolas, monospace',
            letterSpacing: 0,
          },
          ':focus-visible': { outline: `2px solid ${dark ? '#57c7e8' : '#087e9f'}`, outlineOffset: 2 },
          '@media (prefers-reduced-motion: reduce)': {
            '*, *::before, *::after': { scrollBehavior: 'auto !important', transitionDuration: '0.01ms !important' },
          },
        },
      },
      MuiPaper: { styleOverrides: { root: { backgroundImage: 'none' } } },
      MuiAppBar: { styleOverrides: { root: { backgroundImage: 'none' } } },
    },
  })
}
