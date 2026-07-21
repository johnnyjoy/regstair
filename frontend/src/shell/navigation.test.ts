import { describe, expect, it } from 'vitest'

import { navigationFor } from './navigation'

describe('navigationFor', () => {
  it('shows operational and administration pages to administrators', () => {
    expect(navigationFor('admin').map((item) => item.label)).toEqual([
      'Overview',
      'Requests',
      'Routes',
      'Registries',
      'Registry access',
      'Cache',
      'Users',
      'Audit',
    ])
  })

  it('shows only the account workspace to regular users', () => {
    expect(navigationFor('user').map((item) => item.label)).toEqual(['Registry access'])
  })
})
