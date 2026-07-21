import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { ThemeModeProvider } from './ThemeModeProvider'
import { ThemeModeControl } from './ThemeModeControl'

describe('ThemeModeControl', () => {
  it('offers system, light, and dark modes and persists the selected preference', async () => {
    const user = userEvent.setup()
    render(<ThemeModeProvider><ThemeModeControl /></ThemeModeProvider>)
    await user.click(screen.getByRole('button', { name: 'Color theme: system' }))
    expect(screen.getByRole('menuitemradio', { name: 'System theme' })).toHaveAttribute('aria-checked', 'true')
    await user.click(screen.getByRole('menuitemradio', { name: 'Dark theme' }))
    expect(window.localStorage.getItem('regstair-theme')).toBe('dark')
    expect(screen.getByRole('button', { name: 'Color theme: dark' })).toBeVisible()
  })
})
