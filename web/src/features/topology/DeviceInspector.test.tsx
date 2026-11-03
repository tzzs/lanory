import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { DeviceInspector } from '@/features/topology/DeviceInspector'
import { useUIStore } from '@/stores/ui'
import type { Device, DeviceDetail, Topology } from '@/types/api'

const useDeviceMock = vi.fn()
const useTopologyMock = vi.fn()

vi.mock('@/hooks/useApi', () => ({
  useDevice: (...args: unknown[]) => useDeviceMock(...args),
  useTopology: (...args: unknown[]) => useTopologyMock(...args),
}))

const device: Device = {
  id: 'router', name: 'Router', hostname: '', type: 'router', manufacturer: '', model: '', description: '',
  is_manual: false, is_locked: false, is_hidden: false, state: 'online', last_state_change: '',
  primary_mac: 'aa:bb:cc:dd:ee:ff', discovery_sources: [], is_gateway: true, is_self: true,
  first_seen: '', last_seen: '', created_at: '', updated_at: '',
}

const detail: DeviceDetail = {
  device,
  addresses: [
    { id: 'a1', device_id: 'router', address: '192.168.100.1', family: 'ipv4', prefix_length: 24, interface_id: 'lan', source: 'openwrt', is_primary: true, first_seen: '', last_seen: '' },
    { id: 'a2', device_id: 'router', address: '192.168.1.4', family: 'ipv4', prefix_length: 24, interface_id: 'wan', source: 'openwrt', is_primary: false, first_seen: '', last_seen: '' },
  ],
  identities: [],
  interfaces: [
    { id: 'lan', device_id: 'router', name: 'br-lan', type: 'ethernet', role: 'lan', admin_state: 'up', oper_state: 'up', carrier: true, speed_mbps: 1000, mtu: 1500, is_physical: true, rx_bytes: 0, tx_bytes: 0, rx_errors: 0, tx_errors: 0, rx_packets: 0, tx_packets: 0, rx_dropped: 0, tx_dropped: 0, last_seen: '' },
    { id: 'wan', device_id: 'router', name: 'eth0', type: 'ethernet', role: 'wan', admin_state: 'up', oper_state: 'up', carrier: true, speed_mbps: 1000, mtu: 1500, is_physical: true, rx_bytes: 0, tx_bytes: 0, rx_errors: 0, tx_errors: 0, rx_packets: 0, tx_packets: 0, rx_dropped: 0, tx_dropped: 0, last_seen: '' },
  ],
  edges: [], probes: [], last_probe: null, events: [],
}

const child: Device = {
  ...device, id: 'child', name: '客厅音箱', type: 'iot', primary_mac: 'aa:bb:cc:dd:ee:01',
}

function renderInspector() {
  return render(
    <MemoryRouter>
      <DeviceInspector />
    </MemoryRouter>,
  )
}

describe('DeviceInspector', () => {
  beforeEach(() => {
    useDeviceMock.mockReset()
    useTopologyMock.mockReset()
    useDeviceMock.mockReturnValue({ data: detail, isLoading: false, isError: false })
    useTopologyMock.mockReturnValue({ data: null })
    useUIStore.setState({ selectedDeviceId: device.id })
  })

  it('shows the empty panel when no device is selected', () => {
    useUIStore.setState({ selectedDeviceId: null })
    renderInspector()
    expect(screen.getByText('Select a node')).toBeTruthy()
    expect(useDeviceMock).not.toHaveBeenCalled()
  })

  it('shows a loading state while device details are being fetched', () => {
    useDeviceMock.mockReturnValue({ data: undefined, isLoading: true, isError: false })
    renderInspector()
    expect(document.querySelector('aside .animate-pulse')).toBeTruthy()
  })

  it('shows an error state and closes it when requested', async () => {
    useDeviceMock.mockReturnValue({ data: undefined, isLoading: false, isError: true })
    renderInspector()
    expect(screen.getByText('Something went wrong')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Close' }))
    expect(useUIStore.getState().selectedDeviceId).toBeNull()
  })

  it('shows the no-address message and expands the complete address list', async () => {
    useDeviceMock.mockReturnValue({
      data: { ...detail, addresses: [], device: { ...device, is_manual: true, manufacturer: 'Lanory', model: 'Router X', hostname: 'router.local', state_reason: 'offline', state_reason_code: 'probe_failed' } },
      isLoading: false,
      isError: false,
    })
    renderInspector()
    expect(screen.getByText('No address')).toBeTruthy()
    expect(screen.getByText('Manual')).toBeTruthy()
    expect(screen.getByText('Router X')).toBeTruthy()
    expect(screen.getByText('router.local')).toBeTruthy()
    expect(screen.getByText('offline')).toBeTruthy()
    expect(screen.getAllByText('—').length).toBeGreaterThan(0)
    expect(screen.getByRole('button', { name: 'View device details' })).toBeTruthy()
    cleanup()

    useDeviceMock.mockReturnValue({
      data: { ...detail, addresses: [...detail.addresses, { ...detail.addresses[0], id: 'a3', address: '2001:db8::1', family: 'ipv6' }] },
      isLoading: false,
      isError: false,
    })
    useUIStore.setState({ selectedDeviceId: device.id })
    const { unmount } = renderInspector()
    fireEvent.click(screen.getByRole('button', { name: /Show all 3 addresses/ }))
    expect(screen.getByText('2001:db8::1')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: /Show fewer addresses/ }))
    expect(screen.queryByText('2001:db8::1')).toBeNull()
    unmount()
  })

  it('uses the shared address groups to show subnet labels in detail', () => {
    renderInspector()

    // Both sides collapse to one row each; the LAN/WAN subnet labels are on
    // the visible rows.
    expect(screen.getByText(/LAN · 192\.168\.100\.0\/24/)).toBeTruthy()
    expect(screen.getByText(/WAN · 192\.168\.1\.0\/24/)).toBeTruthy()
    // The IPv6 address collapses into the same side group as the IPv4 on that
    // side, so it is not a visible row until the block is expanded.
    expect(screen.queryByText('2001:db8:100::1')).toBeNull()
  })

  it('omits fact rows for values that were never observed', () => {
    renderInspector()

    expect(screen.queryByText('厂商')).toBeNull()
    expect(screen.queryByText('型号')).toBeNull()
  })

  it('names the device at the other end of a link instead of the edge type', () => {
    useDeviceMock.mockReturnValue({
      data: {
        ...detail,
        edges: [{
          id: 'e1', source_id: 'router', target_id: 'child', edge_type: 'observed_mac_port',
          discovery_source: 'bridge_fdb', confidence: 0.6, user_confirmed: false, locked: false,
          status: 'unverified', evidence_count: 3, unverified: true,
          last_observed: '', created_at: '', updated_at: '',
        }],
      },
      isLoading: false,
      isError: false,
    })
    const topology: Topology = {
      nodes: [{ id: 'child', kind: 'device', device: child, state: 'online' }],
      edges: [], layout: [], generated_at: '',
    }
    useTopologyMock.mockReturnValue({ data: topology })

    renderInspector()

    expect(screen.getByText('客厅音箱')).toBeTruthy()
    expect(screen.queryByText('端口学习')).toBeNull()
  })
})
