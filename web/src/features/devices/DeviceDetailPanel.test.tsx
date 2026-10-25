import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { DeviceDetailPanel } from './DeviceDetailPanel'
import { useUIStore } from '@/stores/ui'
import type { Device, DeviceDetail } from '@/types/api'

/**
 * Detail panel layout tests.
 *
 * The panel used to be a fixed 320px column sitting beside the list at every
 * viewport width, which left ~70px for the list at 390px. It is now an overlay
 * drawer below `lg` and the same side column from `lg` up. jsdom applies no
 * CSS, so the responsive behaviour is locked by asserting on the class recipe
 * itself — that is the only observable the DOM offers here.
 */

// The panel reads TanStack Query hooks and the Zustand UI store. Both are
// stubbed so the component can be rendered without a QueryClientProvider or a
// backend; only the layout is under test.
const useDeviceDetail = vi.fn()

vi.mock('@/hooks/useApi', () => ({
  useDevice: (...args: unknown[]) => useDeviceDetail(...args),
  useDevices: () => ({ data: { devices: [] } }),
  usePatchDevice: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useDeleteDevice: () => ({ mutate: vi.fn(), isPending: false }),
  useMergeDevice: () => ({ mutate: vi.fn(), isPending: false }),
  useCreateDevice: () => ({ mutateAsync: vi.fn(), isPending: false }),
}))

const device: Device = {
  id: 'device-a',
  name: 'Living Room AP',
  hostname: 'ap-livingroom',
  type: 'wireless_ap',
  manufacturer: 'Ubiquiti',
  model: 'U6-Lite',
  description: '',
  is_manual: false,
  is_locked: false,
  is_hidden: false,
  state: 'online',
  last_state_change: '2024-05-01T10:00:00Z',
  primary_ipv4: '192.168.1.20',
  primary_mac: 'aa:bb:cc:dd:ee:ff',
  discovery_sources: ['openwrt'],
  is_gateway: false,
  is_self: false,
  first_seen: '2024-04-01T10:00:00Z',
  last_seen: '2024-05-01T10:00:00Z',
  created_at: '2024-04-01T10:00:00Z',
  updated_at: '2024-05-01T10:00:00Z',
}

const detail: DeviceDetail = {
  device,
  addresses: [],
  identities: [],
  interfaces: [],
  edges: [],
  probes: [],
  last_probe: null,
  events: [],
}

/** Resolves the rendered `<aside>` so its class list can be inspected. */
function panelElement(): HTMLElement {
  const panel = document.querySelector('aside')
  if (!panel) throw new Error('the panel rendered no <aside>')
  return panel as HTMLElement
}

/** The panel's action row navigates on delete, so it needs a router. */
function renderPanel(onClose: () => void) {
  return render(
    <MemoryRouter>
      <DeviceDetailPanel onClose={onClose} />
    </MemoryRouter>,
  )
}

describe('DeviceDetailPanel', () => {
  beforeEach(() => {
    useDeviceDetail.mockReset()
    useDeviceDetail.mockReturnValue({ data: detail, isLoading: false, isError: false })
    useUIStore.setState({ selectedDeviceId: device.id })
  })

  it('renders the device name and a close control', () => {
    renderPanel(() => {})
    expect(screen.getByText(device.name)).toBeTruthy()
    expect(screen.getByRole('button', { name: /close|关闭/i })).toBeTruthy()
  })

  it('calls onClose when the close control is clicked', () => {
    const onClose = vi.fn()
    renderPanel(onClose)
    fireEvent.click(screen.getByRole('button', { name: /close|关闭/i }))
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('is an overlay drawer below lg and a side column from lg up', () => {
    renderPanel(() => {})
    const classes = panelElement().className.split(/\s+/)
    // Drawer below `lg`: out of the flex row, pinned right, no wider than a
    // phone-friendly `max-w-sm`.
    expect(classes).toContain('fixed')
    expect(classes).toContain('inset-y-0')
    expect(classes).toContain('right-0')
    expect(classes).toContain('w-full')
    expect(classes).toContain('max-w-sm')
    // Side column from `lg` up: back in the flex row at the original 320px.
    expect(classes).toContain('lg:static')
    expect(classes).toContain('lg:w-80')
    expect(classes).toContain('lg:max-w-none')
    // The panel scrolls on its own rather than growing the page.
    expect(classes).toContain('overflow-y-auto')
  })

  it('uses the same shell while loading, so the panel does not jump', () => {
    useDeviceDetail.mockReturnValue({ data: undefined, isLoading: true, isError: false })
    renderPanel(() => {})
    const classes = panelElement().className.split(/\s+/)
    expect(classes).toContain('fixed')
    expect(classes).toContain('lg:static')
  })

  it('uses the same shell on the error branch', () => {
    useDeviceDetail.mockReturnValue({ data: undefined, isLoading: false, isError: true })
    renderPanel(() => {})
    const classes = panelElement().className.split(/\s+/)
    expect(classes).toContain('fixed')
    expect(classes).toContain('lg:static')
  })
})
