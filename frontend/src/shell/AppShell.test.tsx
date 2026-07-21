import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'

import { AppShell } from './AppShell'
import { ThemeModeProvider } from '../theme/ThemeModeProvider'

describe('AppShell', () => {
  it('provides branded navigation, a skip link, health, and account controls', () => {
    render(
      <MemoryRouter>
        <ThemeModeProvider>
        <AppShell role="admin" username="james" health="healthy" onSignOut={vi.fn()}>
          <h1>Operational overview</h1>
        </AppShell>
        </ThemeModeProvider>
      </MemoryRouter>,
    )

    expect(screen.getByRole('link', { name: 'Skip to main content' })).toHaveAttribute('href', '#main-content')
    expect(screen.getByRole('link', { name: 'Regstair overview' })).toBeVisible()
    expect(screen.getByRole('link', { name: 'Regstair overview' }).querySelector('img')).toHaveAttribute('src', '/regstair-logo.png')
    expect(screen.getByRole('navigation', { name: 'Primary navigation' })).toBeVisible()
    expect(screen.getByText('Healthy')).toBeVisible()
    expect(screen.getByRole('button', { name: /james account menu/i })).toBeVisible()
    expect(screen.getByRole('main')).toContainElement(screen.getByRole('heading', { name: 'Operational overview' }))
  })

  it('opens the compact navigation from an explicitly named control', async () => {
    const user = userEvent.setup()
    render(
      <MemoryRouter>
        <ThemeModeProvider>
        <AppShell role="admin" username="james" health="degraded" compact onSignOut={vi.fn()}>
          <div>Content</div>
        </AppShell>
        </ThemeModeProvider>
      </MemoryRouter>,
    )

    expect(screen.queryByRole('navigation', { name: 'Primary navigation' })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Open navigation' }))
    expect(screen.getByRole('navigation', { name: 'Primary navigation' })).toBeVisible()
  })

  it('signs out from the account menu', async () => {
    const user = userEvent.setup()
    const onSignOut = vi.fn()
    render(
      <MemoryRouter>
        <ThemeModeProvider>
        <AppShell role="user" username="alice" health="healthy" onSignOut={onSignOut}>
          <div>Content</div>
        </AppShell>
        </ThemeModeProvider>
      </MemoryRouter>,
    )

    await user.click(screen.getByRole('button', { name: /alice account menu/i }))
    await user.click(screen.getByRole('menuitem', { name: 'Sign out' }))
    expect(onSignOut).toHaveBeenCalledOnce()
  })
})
