import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { OverviewView, type OverviewModel } from './OverviewPage'

const healthy: OverviewModel = {
  requests: [
    { id: 9, timestamp: '2026-07-21T12:00:00Z', operation: 'pull', logical_reference: 'library/alpine:edge', status: 'success', cache_result: 'hit', matched_route: 'public', source_or_destination: 'docker-hub', duration: 12000000 },
    { id: 8, timestamp: '2026-07-21T11:59:00Z', operation: 'push', logical_reference: 'team/api:1.0', status: 'denied', cache_result: '', matched_route: 'team', source_or_destination: 'harbor', duration: 3000000 },
  ],
  sources: [{ id: 'docker-hub', name: 'Docker Hub' }, { id: 'harbor', name: 'Harbor' }],
  sourceHealth: [{ id: 'docker-hub', status: 'healthy' }, { id: 'harbor', status: 'healthy' }],
  routeCount: 3,
  artifactCount: 14,
  blobs: [{ digest: 'sha256:a', size: 52428800 }],
}

function renderView(model: OverviewModel) {
  return render(<MemoryRouter><ThemeModeProvider><OverviewView model={model} /></ThemeModeProvider></MemoryRouter>)
}

describe('OverviewView', () => {
  it('summarizes operational state and makes the request path visible', () => {
    renderView(healthy)

    expect(screen.getByRole('heading', { name: 'Registry status' })).toBeVisible()
    expect(screen.getByText('2')).toBeVisible()
    expect(screen.getByText('100%')).toBeVisible()
    expect(screen.getByText('50 MB')).toBeVisible()
    expect(screen.getByLabelText('Registry request path')).toHaveTextContent('ClientsRegstairRoutingCacheRegistries')
    expect(screen.getByRole('link', { name: /library\/alpine:edge/i })).toHaveAttribute('href', '/requests/9')
  })

  it('calls out degraded registries as an actionable condition', () => {
    renderView({ ...healthy, sourceHealth: [{ id: 'docker-hub', status: 'unavailable' }, { id: 'harbor', status: 'healthy' }] })

    expect(screen.getByRole('status')).toHaveTextContent('1 registry needs attention')
    expect(screen.getByRole('link', { name: 'Review registries' })).toHaveAttribute('href', '/sources')
  })

  it('gives an empty installation a useful starting state', () => {
    renderView({ requests: [], sources: [], sourceHealth: [], routeCount: 0, artifactCount: 0, blobs: [] })

    expect(screen.getByText('No registry traffic yet')).toBeVisible()
    expect(screen.getByText(/configure a client to use this Regstair address/i)).toBeVisible()
  })
})
