import { render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { DeviceInspector } from '@/features/topology/DeviceInspector'
import { useUIStore } from '@/stores/ui'
import type { Device, DeviceDetail } from '@/types/api'

const useDeviceMock = vi.fn()

vi.mock('@/hooks/useApi', () => ({
  useDevice: (...args: unknown[]) => useDeviceMock(...args),
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

describe('DeviceInspector', () => {
  beforeEach(() => {
    useDeviceMock.mockReset()
    useDeviceMock.mockReturnValue({ data: detail, isLoading: false, isError: false })
    useUIStore.setState({ selectedDeviceId: device.id })
  })

  it('uses the shared address groups to show subnet labels in detail', () => {
    render(<DeviceInspector />)

    expect(screen.getByText(/LAN · 192\.168\.100\.0\/24/)).toBeTruthy()
    expect(screen.getByText(/WAN · 192\.168\.1\.0\/24/)).toBeTruthy()
  })
})
