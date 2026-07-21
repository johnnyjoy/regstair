import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { AccountView } from './AccountPage'

describe('AccountView', () => {
  it('presents user-owned Docker and upstream credential workflows', () => {
    render(<ThemeModeProvider><AccountView account={{ username: 'alice', display_name: 'Alice', access: 'user' }} tokens={[]} credentials={[]} registries={[{ id: 'harbor', name: 'Team Harbor', endpoint: 'https://harbor.example', pull: true, push: true, routes: ['team'] }]} credentialsAvailable onPassword={vi.fn()} onCreateToken={vi.fn()} onRevokeToken={vi.fn()} onSaveCredential={vi.fn()} onRemoveCredential={vi.fn()} /></ThemeModeProvider>)
    expect(screen.getByRole('heading', { name: 'Account' })).toBeVisible()
    expect(screen.getByRole('heading', { name: 'Docker access' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Create token' })).toBeVisible()
    expect(screen.getByText('Team Harbor')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Add credential' })).toBeVisible()
  })
})
