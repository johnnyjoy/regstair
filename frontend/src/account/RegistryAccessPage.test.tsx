import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { RegistryAccessView } from './RegistryAccessPage'

describe('RegistryAccessView', () => {
  it('makes registry connection status and the connect workflow explicit', async () => {
    const user = userEvent.setup()
    const onSave = vi.fn()
    render(<ThemeModeProvider><RegistryAccessView credentials={[]} registries={[{ id: 'harbor', name: 'Harbor', endpoint: 'harbor.example.test', pull: true, push: true }]} credentialsAvailable onSave={onSave} onRemove={vi.fn()} /></ThemeModeProvider>)

    expect(screen.getByRole('heading', { name: 'Registry access' })).toBeVisible()
    expect(screen.getByText(/public pulls may still work without an account/i)).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Connect' }))
    await user.type(screen.getByRole('textbox', { name: /registry username/i }), 'alice')
    await user.type(screen.getByLabelText(/password, access token, or robot secret/i), 'secret-value')
    await user.click(screen.getByRole('button', { name: 'Verify and connect' }))

    expect(onSave).toHaveBeenCalledWith('harbor', 'alice', 'secret-value')
  })
})
