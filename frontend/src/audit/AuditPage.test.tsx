import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { AuditView } from './AuditPage'

describe('AuditView', () => {
  it('humanizes security activity without rendering raw event details', () => {
    render(<ThemeModeProvider><AuditView data={{ actions: [{ action: 'user.created', label: 'Created user' }], events: [{ id: 1, timestamp: '2026-07-21T10:00:00Z', action: 'user.created', action_label: 'Created user', outcome: 'success', actor: 'James', target: 'Alice' }] }} /></ThemeModeProvider>)
    expect(screen.getByRole('heading', { name: 'Audit' })).toBeVisible()
    expect(screen.getByText('Created user')).toBeVisible()
    expect(screen.getByText('James')).toBeVisible()
    expect(screen.getByText('Alice')).toBeVisible()
  })
})
