import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { CacheView } from './CachePage'

const artifacts = [
  { logical_reference: 'library/alpine:edge', mapping: { logical_repository: 'library/alpine', tag: 'edge', digest: 'sha256:aaa', media_type: 'application/vnd.oci.image.manifest.v1+json', size: 30 * 1024 * 1024, blob_digests: ['sha256:shared', 'sha256:a'], source: 'docker-hub', route: 'public', resolved_at: '2026-07-21T12:00:00Z', last_validated_at: '2026-07-21T12:05:00Z', fresh_until: '2099-07-21T13:00:00Z' } },
  { logical_reference: 'library/nginx:1.27', mapping: { logical_repository: 'library/nginx', tag: '1.27', digest: 'sha256:bbb', media_type: 'application/vnd.oci.image.manifest.v1+json', size: 40 * 1024 * 1024, blob_digests: ['sha256:shared', 'sha256:b'], source: 'docker-hub', route: 'public', resolved_at: '2026-07-21T12:00:00Z', last_validated_at: '2026-07-21T12:05:00Z', fresh_until: '2020-01-01T00:00:00Z' } },
]
const blobs = [{ digest: 'sha256:shared', size: 10 * 1024 * 1024 }, { digest: 'sha256:a', size: 20 * 1024 * 1024 }, { digest: 'sha256:b', size: 30 * 1024 * 1024 }]

describe('CacheView', () => {
  it('shows physical capacity, logical content, deduplication, and freshness', () => {
    render(<MemoryRouter><ThemeModeProvider><CacheView artifacts={artifacts} blobs={blobs} /></ThemeModeProvider></MemoryRouter>)
    expect(screen.getByRole('heading', { name: 'Cache' })).toBeVisible()
    expect(screen.getByText('60 MB')).toBeVisible()
    expect(screen.getByText('70 MB')).toBeVisible()
    expect(screen.getByText('10 MB')).toBeVisible()
    expect(screen.getByText('Fresh')).toBeVisible()
    expect(screen.getByText('Stale')).toBeVisible()
  })

  it('filters artifacts without hiding capacity context', async () => {
    const user = userEvent.setup()
    render(<MemoryRouter><ThemeModeProvider><CacheView artifacts={artifacts} blobs={blobs} /></ThemeModeProvider></MemoryRouter>)
    await user.type(screen.getByRole('searchbox', { name: 'Search cached artifacts' }), 'alpine')
    expect(screen.getByText('library/alpine:edge')).toBeVisible()
    expect(screen.queryByText('library/nginx:1.27')).not.toBeInTheDocument()
    expect(screen.getByText('60 MB')).toBeVisible()
  })
})
