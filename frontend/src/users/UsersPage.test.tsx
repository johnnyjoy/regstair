import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'
import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { UsersView } from './UsersPage'

describe('UsersView', () => {
  it('shows identity, access, state, and protected administration actions', () => {
    render(<MemoryRouter><ThemeModeProvider><UsersView users={[{ id: '1', username: 'admin', display_name: 'James', email: 'james@example.test', access: 'admin', enabled: true, ctime: '2026-01-01T00:00:00Z', mtime: '2026-01-01T00:00:00Z' }]} onCreate={vi.fn()} onSave={vi.fn()} onReset={vi.fn()} /></ThemeModeProvider></MemoryRouter>)
    expect(screen.getByRole('heading', { name: 'Users' })).toBeVisible()
    expect(screen.getByText('James')).toBeVisible()
    expect(screen.getByRole('combobox', { name: 'Access for admin' })).toHaveTextContent('Administrator')
    expect(screen.getByRole('checkbox', { name: 'Enabled for admin' })).toBeChecked()
    expect(screen.getByRole('button', { name: 'Reset password for admin' })).toBeVisible()
    expect(screen.getByText(/role, access, and password changes end that user's active sessions immediately/i)).toBeVisible()
  })
})
