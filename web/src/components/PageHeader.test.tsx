import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { PageHeader, SectionHeader } from './ui'

/**
 * The page header is what gives every route an `h1`. Before it existed the
 * document had no level-one heading at all: each page started at `h2`, so a
 * screen reader's outline began at the second level and the "jump to the main
 * heading" command had nothing to land on. These tests lock the level, not the
 * styling, because the level is the part that regresses silently.
 */

describe('PageHeader', () => {
  it('renders the title as the page-level heading', () => {
    render(<PageHeader title="Devices" />)

    const heading = screen.getByRole('heading', { level: 1 })
    expect(heading).toHaveTextContent('Devices')
  })

  it('renders the description when one is provided', () => {
    render(<PageHeader title="Devices" description="12 devices" />)

    expect(screen.getByText('12 devices')).toBeTruthy()
  })

  it('omits the description element when none is provided', () => {
    const { container } = render(<PageHeader title="Devices" />)

    // The description is a `p`; with no description there is no paragraph at
    // all, rather than an empty one that would add a gap and read as blank.
    expect(container.querySelector('p')).toBeNull()
  })

  it('renders the actions slot', () => {
    render(<PageHeader title="Devices" actions={<button>Add device</button>} />)

    expect(screen.getByRole('button', { name: 'Add device' })).toBeTruthy()
  })
})

describe('SectionHeader', () => {
  it('renders as the second-level heading under a page header', () => {
    render(
      <>
        <PageHeader title="Monitoring" />
        <SectionHeader title="LAN / WAN" />
      </>,
    )

    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Monitoring')
    expect(screen.getByRole('heading', { level: 2 })).toHaveTextContent('LAN / WAN')
  })

  it('renders the description and actions when provided', () => {
    render(<SectionHeader title="Probes" description="6 targets" actions={<button>Refresh</button>} />)

    expect(screen.getByText('6 targets')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Refresh' })).toBeTruthy()
  })
})
