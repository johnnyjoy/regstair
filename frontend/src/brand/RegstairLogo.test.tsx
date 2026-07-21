import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { RegstairLogo } from './RegstairLogo'

describe('RegstairLogo', () => {
  it('uses the preserved logo asset with accessible text when meaningful', () => {
    render(<RegstairLogo />)
    expect(screen.getByRole('img', { name: 'Regstair logo' })).toHaveAttribute('src', '/regstair-logo.png')
  })

  it('is hidden from assistive technology inside an already-labelled brand link', () => {
    const { container } = render(<RegstairLogo compact decorative />)
    expect(container.querySelector('img')).toHaveAttribute('alt', '')
    expect(screen.queryByRole('img')).not.toBeInTheDocument()
  })
})
