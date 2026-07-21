import { describe, expect, it } from 'vitest'

import { resolveThemeMode } from './themeMode'

describe('resolveThemeMode', () => {
  it('uses an explicit light or dark preference', () => {
    expect(resolveThemeMode('dark', false)).toBe('dark')
    expect(resolveThemeMode('light', true)).toBe('light')
  })

  it('uses the operating-system preference for system mode', () => {
    expect(resolveThemeMode('system', true)).toBe('dark')
    expect(resolveThemeMode('system', false)).toBe('light')
  })

  it('treats an unknown persisted value as system mode', () => {
    expect(resolveThemeMode('unexpected', true)).toBe('dark')
  })
})
