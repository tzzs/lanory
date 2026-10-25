import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, act, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { DevicesPage } from './DevicesPage'
import { api } from '@/lib/api'
import { t } from '@/i18n'
import type { Device, DeviceList } from '@/types/api'

/**
 * Device table focus test.
 *
 * The table re-renders on every poll and on every filter keystroke. It used to
 * replace the rows with a skeleton (or with the error state, when a poll
 * failed), which unmounts the row a keyboard user is standing on and drops
 * focus to `<body>` — every refresh cost the user their place. The rows are now
 * kept mounted across a background refetch, and a failed or emptied refetch is
 * reported beside them instead of in place of them.
 *
 * The table is driven through a real QueryClient so the flags TanStack actually
 * produces (`isLoading` false but `isFetching` true during a refetch) are the
 * ones exercised, rather than a hand-written stand-in.
 */

const NOW = new Date().toISOString()
// One month newer: enough to make the rendered "last seen" cell differ, which
// is how the test proves the *refreshed* payload reached the screen.
const LATER = new Date(Date.now() + 30 * 86_400_000).toISOString()

function device(id: string, name: string, lastSeen: string): Device {
  return {
    id,
    name,
    hostname: name,
    type: 'desktop',
    manufacturer: '',
    model: '',
    description: '',
    is_manual: false,
    is_locked: false,
    is_hidden: false,
    state: 'online',
    last_state_change: NOW,
    primary_ipv4: '192.168.1.20',
    primary_mac: 'aa:bb:cc:dd:ee:ff',
    discovery_sources: ['openwrt'],
    is_gateway: false,
    is_self: false,
    first_seen: NOW,
    last_seen: lastSeen,
    created_at: NOW,
    updated_at: NOW,
  }
}

const firstPage: DeviceList = {
  devices: [device('a', 'Alpha', NOW), device('b', 'Beta', NOW)],
  total: 2,
  limit: 200,
  offset: 0,
}
// Same ids, newer timestamps: exactly what a poll returns.
const secondPage: DeviceList = {
  devices: [device('a', 'Alpha', LATER), device('b', 'Beta', LATER)],
  total: 2,
  limit: 200,
  offset: 0,
}

const devicesMock = api.devices as unknown as ReturnType<typeof vi.fn>

// The API client is the only boundary that has to be faked: the page's real
// hooks, query keys and TanStack flags are all kept, so what is exercised is
// the production refetch path rather than a stand-in for it.
vi.mock('@/lib/api', () => ({
  api: { devices: vi.fn(), device: vi.fn() },
}))

function renderPage(queryClient: QueryClient) {
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>
        <DevicesPage />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

function newQueryClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

/** The device table row for `name` (a `tr` with a `role="button"` label). */
function row(name: string): HTMLElement {
  return screen.getByRole('button', { name })
}

describe('DevicesPage table focus', () => {
  beforeEach(() => {
    devicesMock.mockReset()
    ;(api.device as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({ device: firstPage.devices[0] })
  })

  /**
   * A poll, in miniature: the same query key is refetched with a fresh
   * payload, which is the state where `isFetching` is true and `isLoading` is
   * not. `refetch` alone does nothing while the data is still fresh, so the
   * query is invalidated first — exactly what the SSE handler and the poll do.
   */
  async function refetch(queryClient: QueryClient) {
    await act(async () => {
      await queryClient.invalidateQueries({ queryKey: ['devices'] })
    })
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20))
    })
  }

  it('keeps the focused row mounted and focused across a background refetch', async () => {
    devicesMock.mockResolvedValue(firstPage)
    const queryClient = newQueryClient()
    renderPage(queryClient)
    await waitFor(() => expect(screen.getByText('Alpha')).toBeTruthy())

    const focused = row('Alpha')
    focused.focus()
    expect(document.activeElement).toBe(focused)
    const table = document.querySelector('table')

    // A poll: same query key, fresh payload, `isFetching` without `isLoading`.
    devicesMock.mockResolvedValue(secondPage)
    await refetch(queryClient)

    // The row is the *same DOM node*, so focus was never dropped ...
    expect(document.activeElement).toBe(focused)
    expect(row('Alpha')).toBe(focused)
    // ... and neither was the table: no remount, no scroll reset.
    expect(document.querySelector('table')).toBe(table)
    // The refreshed payload did reach the screen: the "last seen" cell moved
    // from "now" to "in 30 days".
    expect(focused.textContent).toContain('in 30 days')
  })

  it('shows the skeleton only on the first load, not while refetching', async () => {
    devicesMock.mockResolvedValue(firstPage)
    const queryClient = newQueryClient()
    renderPage(queryClient)
    await waitFor(() => expect(screen.getByText('Alpha')).toBeTruthy())
    expect(document.querySelector('.animate-pulse')).toBeNull()

    devicesMock.mockResolvedValue(secondPage)
    await refetch(queryClient)
    // Still no skeleton: a refetch must not destroy the table.
    expect(document.querySelector('.animate-pulse')).toBeNull()
    expect(row('Alpha')).toBeTruthy()
  })

  it('keeps the rows while a filter change is still fetching', async () => {
    devicesMock.mockResolvedValue(firstPage)
    const queryClient = newQueryClient()
    renderPage(queryClient)
    await waitFor(() => expect(screen.getByText('Alpha')).toBeTruthy())

    const focused = row('Alpha')
    focused.focus()
    const table = document.querySelector('table')

    // A filter keystroke changes the query key, so the new key has no data of
    // its own yet — the placeholder keeps the previous page on screen. The
    // skeleton must stay out of it: it used to replace the rows here too.
    let release: (value: DeviceList) => void = () => {}
    devicesMock.mockReturnValueOnce(new Promise<DeviceList>((resolve) => { release = resolve }))
    fireEvent.change(screen.getByLabelText(t('app.search')), { target: { value: 'Al' } })

    await waitFor(() => expect(devicesMock).toHaveBeenCalledTimes(2))
    expect(document.activeElement).toBe(focused)
    expect(document.querySelector('table')).toBe(table)
    expect(document.querySelector('.animate-pulse')).toBeNull()

    await act(async () => {
      release(secondPage)
      await new Promise((resolve) => setTimeout(resolve, 20))
    })
    expect(document.querySelector('.animate-pulse')).toBeNull()
  })

  it('keeps the rows when a background refetch fails, and reports it beside them', async () => {
    devicesMock.mockResolvedValue(firstPage)
    const queryClient = newQueryClient()
    renderPage(queryClient)
    await waitFor(() => expect(screen.getByText('Alpha')).toBeTruthy())

    const focused = row('Alpha')
    focused.focus()

    devicesMock.mockRejectedValue(new Error('router unreachable'))
    await refetch(queryClient)

    // The stale-but-usable rows survive, and so does focus.
    expect(row('Alpha')).toBe(focused)
    expect(document.activeElement).toBe(focused)
    // The failure is still reported, with a way to retry.
    expect(screen.getByRole('alert')).toBeTruthy()
    expect(screen.getByText('router unreachable')).toBeTruthy()
    expect(screen.getByRole('button', { name: /retry/i })).toBeTruthy()
  })

  it('still shows the error state when the first load fails', async () => {
    devicesMock.mockRejectedValue(new Error('router unreachable'))
    renderPage(newQueryClient())
    await waitFor(() => expect(screen.getByRole('alert')).toBeTruthy())
    // Nothing to preserve yet, so the card owns the whole message.
    expect(screen.queryByRole('button', { name: 'Alpha' })).toBeNull()
  })

  it('gives each row a stable key so a refresh reuses the DOM node', async () => {
    devicesMock.mockResolvedValue(firstPage)
    const queryClient = newQueryClient()
    renderPage(queryClient)
    await waitFor(() => expect(screen.getByText('Alpha')).toBeTruthy())

    const before = row('Alpha')
    devicesMock.mockResolvedValue(secondPage)
    await refetch(queryClient)
    expect(row('Alpha')).toBe(before)
  })
})
