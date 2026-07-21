export type ThemePreference = 'system' | 'light' | 'dark'
export type ResolvedThemeMode = 'light' | 'dark'

export function isThemePreference(value: string | null): value is ThemePreference {
  return value === 'system' || value === 'light' || value === 'dark'
}

export function resolveThemeMode(value: string | null, systemPrefersDark: boolean): ResolvedThemeMode {
  const preference = isThemePreference(value) ? value : 'system'
  if (preference === 'system') {
    return systemPrefersDark ? 'dark' : 'light'
  }
  return preference
}
