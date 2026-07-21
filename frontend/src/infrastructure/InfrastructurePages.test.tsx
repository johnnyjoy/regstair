import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { RegistriesView, RoutesView } from './InfrastructurePages'

const routes = [{ name: 'curated', match: 'library/', precedence: 10, pull: { sources: ['internal', 'docker-hub'], authoritative: 'internal', external_fallback: true }, push: { destination: 'internal', deny: false }, rewrite: { strip_prefix: 'library/', add_prefix: 'mirror/' } }]

describe('infrastructure views', () => {
  it('explains route ordering, fallback, push, and rewrite behavior', () => {
    render(<MemoryRouter><ThemeModeProvider><RoutesView routes={routes} /></ThemeModeProvider></MemoryRouter>)
    expect(screen.getByRole('heading', { name: 'Routes' })).toBeVisible()
    expect(screen.getByLabelText('curated route path')).toHaveTextContent('internaldocker-hub')
    expect(screen.getByText('internal', { selector: 'strong' })).toBeVisible()
    expect(screen.getByText('library/ → mirror/')).toBeVisible()
  })

  it('connects registry health and route usage', () => {
    render(<MemoryRouter><ThemeModeProvider><RegistriesView sources={[{ id: 'internal', name: 'Harbor', endpoint: 'https://harbor.example', type: 'oci', enabled: true, routes: ['curated'], user_credentials: { pull: true, push: true } }]} health={[{ id: 'internal', status: 'healthy' }]} /></ThemeModeProvider></MemoryRouter>)
    expect(screen.getByRole('heading', { name: 'Registries' })).toBeVisible()
    expect(screen.getByText('Harbor')).toBeVisible()
    expect(screen.getByText('healthy')).toBeVisible()
    expect(screen.getByText('Pull and push credentials')).toBeVisible()
  })
})
