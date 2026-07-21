import { describe, expect, it } from 'vitest'

import { requestQueryFromForm } from './requestFilters'

describe('requestQueryFromForm', () => {
  it('keeps meaningful filters and drops empty/default values', () => {
    const form = new FormData()
    form.set('reference', 'library/alpine')
    form.set('operation', 'pull')
    form.set('status', '')
    form.set('window', '24h')
    form.set('limit', '25')

    expect(requestQueryFromForm(form).toString()).toBe('reference=library%2Falpine&operation=pull&window=24h&limit=25')
  })

  it('never carries an old cursor into a new investigation', () => {
    const form = new FormData()
    form.set('reference', 'team/api')
    form.set('cursor', 'stale-page')

    expect(requestQueryFromForm(form).has('cursor')).toBe(false)
  })
})
