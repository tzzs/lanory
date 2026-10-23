import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { Navigation } from './Navigation'

describe('shared top navigation', () => {
  it('retains every page and selects the device tab for device details', () => {
    const { container } = render(<MemoryRouter initialEntries={['/devices/device-a']}><Navigation embedded /></MemoryRouter>)
    const links = screen.getAllByRole('link')
    expect(links.map(a => a.getAttribute('href'))).toEqual(['/', '/topology', '/devices', '/monitoring', '/events', '/settings'])
    expect(links[2]).toHaveAttribute('aria-current', 'page')
    expect(container.querySelector('aside')).toBeNull()
    expect(screen.queryByRole('heading')).toBeNull()
  })

  it('adds branding in the independent entry', () => {
    render(<MemoryRouter><Navigation embedded={false} /></MemoryRouter>)
    expect(screen.getByRole('heading')).toHaveTextContent('Lanory')
    expect(screen.getAllByRole('link')[0]).toHaveAttribute('aria-current', 'page')
  })
})
