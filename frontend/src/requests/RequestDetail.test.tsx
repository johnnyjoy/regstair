import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { RequestDetailView } from './RequestDetail'

describe('RequestDetailView', () => {
  it('starts with an investigation summary and reveals technical evidence on demand', async () => {
    const user = userEvent.setup()
    render(<MemoryRouter><ThemeModeProvider><RequestDetailView detail={{
      request: { id: 42, timestamp: '2026-07-21T12:00:00Z', operation: 'push', logical_reference: 'team/api:1.0', status: 'error', client_identity: 'alice', matched_route: 'team', source_or_destination: 'harbor', cache_result: 'bypassed', credential: 'Current user credential', duration_ms: 1250, bytes_transferred: 4096, error_classification: 'upstream_authentication_failed', explanation: ['Matched team route', 'Upstream rejected authentication'] },
      provenance: { source: 'harbor', route: 'team', resolved_digest: 'sha256:abc' },
    }} /></ThemeModeProvider></MemoryRouter>)

    expect(screen.getByRole('heading', { name: 'team/api:1.0' })).toBeVisible()
    expect(screen.getByText('Current user credential')).toBeVisible()
    expect(screen.getByText('sha256:abc')).not.toBeVisible()
    await user.click(screen.getByText('Technical evidence'))
    expect(screen.getByText('sha256:abc')).toBeVisible()
    expect(screen.getByText(/Upstream rejected authentication/)).toBeVisible()
  })
})
