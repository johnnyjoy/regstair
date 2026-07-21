import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { EmptyState, Metric, MetricStrip, OperationalPanel, PageHeader, StatusSummary } from './OperationalUI'

describe('operational design primitives', () => {
  it('provides a consistent semantic hierarchy for operational pages', () => {
    render(<ThemeModeProvider><><PageHeader title="Cache" subtitle="Stored content" /><MetricStrip><Metric label="Physical storage" value="2.4 GB" detail="42 blobs" /></MetricStrip><StatusSummary tone="good" title="Cache available" detail="Content store is responding" /><OperationalPanel title="Artifacts"><EmptyState title="No artifacts" detail="Pulled content appears here." /></OperationalPanel></></ThemeModeProvider>)
    expect(screen.getByRole('heading', { level: 1, name: 'Cache' })).toBeVisible()
    expect(screen.getByRole('status')).toHaveTextContent('Cache available')
    expect(screen.getByRole('heading', { level: 2, name: 'Artifacts' })).toBeVisible()
    expect(screen.getByText('2.4 GB')).toBeVisible()
  })
})
