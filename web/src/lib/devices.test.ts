import { describe, expect, it } from 'vitest'
import { formatSpeed, formatBytes, formatPercent, formatLatency, confidenceBand, formatConfidence, formatEdgeLabel, formatRelativeTime, formatMac, formatDuration, groupAddresses, isMultiHomed, addressSummary, groupDeviceAddresses } from '@/lib/devices'
import type { NodeAddress } from '@/types/api'

describe('formatSpeed', () => {
  it('renders Mbps below 1000', () => {
    expect(formatSpeed(100)).toBe('100 Mbps')
    expect(formatSpeed(2500)).toBe('2.5 Gbps')
    expect(formatSpeed(1000)).toBe('1 Gbps')
  })

  it('renders an em dash when there is no negotiated speed', () => {
    expect(formatSpeed(0)).toBe('—')
    expect(formatSpeed(undefined)).toBe('—')
  })
})

describe('formatBytes', () => {
  it('scales units', () => {
    expect(formatBytes(0)).toBe('—')
    expect(formatBytes(512)).toBe('512 B')
    expect(formatBytes(2048)).toBe('2 KB')
    expect(formatBytes(5 * 1024 * 1024)).toBe('5 MB')
  })
})

describe('formatPercent', () => {
  it('keeps small losses visible instead of rounding to zero', () => {
    expect(formatPercent(0)).toBe('0%')
    expect(formatPercent(0.05)).toBe('<0.1%')
    expect(formatPercent(12.34)).toBe('12.3%')
  })
})

describe('formatLatency', () => {
  it('distinguishes sub-millisecond from absent', () => {
    expect(formatLatency(0)).toBe('—')
    expect(formatLatency(0.4)).toBe('<1 ms')
    expect(formatLatency(4.6)).toBe('5 ms')
  })
})

describe('confidence', () => {
  it('bands confidence', () => {
    expect(confidenceBand(0.95)).toBe('high')
    expect(confidenceBand(0.7)).toBe('medium')
    expect(confidenceBand(0.2)).toBe('low')
  })

  it('formats as a clamped percentage', () => {
    expect(formatConfidence(0.855)).toBe('86%')
    expect(formatConfidence(1.4)).toBe('100%')
    expect(formatConfidence(-1)).toBe('0%')
  })
})

describe('formatEdgeLabel', () => {
  it('shows port then speed, matching the spec example', () => {
    expect(formatEdgeLabel({ port: 'eth2', speedMbps: 2500 })).toBe('eth2 · 2.5 Gbps')
    expect(formatEdgeLabel({ port: 'eth1', speedMbps: 1000 })).toBe('eth1 · 1 Gbps')
  })

  it('omits missing parts', () => {
    expect(formatEdgeLabel({})).toBe('')
    expect(formatEdgeLabel({ port: 'eth3' })).toBe('eth3')
    expect(formatEdgeLabel({ label: 'manual' })).toBe('manual')
  })
})

describe('formatMac', () => {
  it('upper-cases for display', () => {
    expect(formatMac('02:11:22:33:44:b7')).toBe('02:11:22:33:44:B7')
    expect(formatMac(undefined)).toBe('—')
  })
})

describe('formatDuration', () => {
  it('renders compact durations', () => {
    expect(formatDuration(30)).toBe('30s')
    expect(formatDuration(90)).toBe('1m 30s')
    expect(formatDuration(3700)).toBe('1h 1m')
    expect(formatDuration(90000)).toBe('1d 1h')
  })
})

describe('formatRelativeTime', () => {
  it('handles missing and invalid input', () => {
    expect(formatRelativeTime(undefined)).toBe('—')
    expect(formatRelativeTime('not-a-date')).toBe('—')
    expect(formatRelativeTime('0001-01-01T00:00:00Z')).toBe('—')
  })

  it('renders a relative string for a past timestamp', () => {
    const past = new Date(Date.now() - 5 * 60_000).toISOString()
    const result = formatRelativeTime(past)
    expect(result).toMatch(/minute|分钟/)
  })

  it.each([
    [30_000, '30 seconds ago'],
    [300_000, '5 minutes ago'],
    [7_200_000, '2 hours ago'],
    [259_200_000, '3 days ago'],
    [5_184_000_000, '2 months ago'],
    [-300_000, 'in 5 minutes'],
  ])('preserves time direction for offset %s', (offset, expected) => {
    expect(formatRelativeTime(new Date(Date.now() - offset).toISOString(), 'en')).toBe(expected)
  })
})

describe('groupAddresses', () => {
  const nodeAddress = (over: Partial<NodeAddress>): NodeAddress => ({
    address: '192.168.100.1',
    family: 'ipv4',
    side: 'unknown',
    ...over,
  })

  it('keeps a single-subnet device in one group', () => {
    const groups = groupAddresses([
      nodeAddress({ address: '192.0.2.10', cidr: '192.0.2.0/24' }),
      nodeAddress({ address: '192.0.2.11', cidr: '192.0.2.0/24' }),
    ])
    expect(groups).toHaveLength(1)
    expect(groups[0].addresses).toHaveLength(2)
  })

  it('splits a multi-homed router by subnet and side, LAN first', () => {
    // The case this exists for: 192.168.100.1 is the LAN side, 192.168.1.4 the
    // WAN side, and they are the same device.
    const groups = groupAddresses([
      nodeAddress({ address: '192.168.1.4', cidr: '192.168.1.0/24', side: 'wan', interface: 'eth0' }),
      nodeAddress({ address: '192.168.100.1', cidr: '192.168.100.0/24', side: 'lan', interface: 'br-lan', is_primary: true }),
    ])
    expect(groups).toHaveLength(2)
    expect(groups[0].side).toBe('lan')
    expect(groups[0].cidr).toBe('192.168.100.0/24')
    expect(groups[1].side).toBe('wan')
    expect(groups[1].cidr).toBe('192.168.1.0/24')
  })

  it('puts the primary address first inside a group', () => {
    const groups = groupAddresses([
      nodeAddress({ address: '192.0.2.11', cidr: '192.0.2.0/24' }),
      nodeAddress({ address: '192.0.2.10', cidr: '192.0.2.0/24', is_primary: true }),
    ])
    expect(groups[0].addresses[0].address).toBe('192.0.2.10')
  })

  it('does not claim a shared subnet when the prefix is unknown', () => {
    const groups = groupAddresses([
      nodeAddress({ address: '192.0.2.10' }),
      nodeAddress({ address: '192.0.2.11' }),
    ])
    expect(groups).toHaveLength(2)
  })

  it('keeps unknown-prefix groups distinguishable in the rendered list', () => {
    const groups = groupAddresses([
      nodeAddress({ address: '192.0.2.10' }),
      nodeAddress({ address: '192.0.2.11' }),
    ])
    expect(groups.map((group) => group.addresses[0].address)).toEqual(['192.0.2.10', '192.0.2.11'])
  })
})

describe('isMultiHomed / addressSummary', () => {
  it('reports a single-address device as not multi-homed', () => {
    expect(isMultiHomed([{ address: '192.0.2.5', family: 'ipv4', side: 'unknown', cidr: '192.0.2.0/24' }])).toBe(false)
  })

  it('reports a router with two subnets as multi-homed', () => {
    expect(
      isMultiHomed([
        { address: '192.168.100.1', family: 'ipv4', side: 'lan', cidr: '192.168.100.0/24' },
        { address: '192.168.1.4', family: 'ipv4', side: 'wan', cidr: '192.168.1.0/24' },
      ]),
    ).toBe(true)
  })

  it('does not call unknown-prefix addresses multi-homed', () => {
    expect(
      isMultiHomed([
        { address: '192.0.2.10', family: 'ipv4', side: 'unknown' },
        { address: '2001:db8::10', family: 'ipv6', side: 'unknown' },
      ]),
    ).toBe(false)
  })

  it('shows a count when more than two subnets are present', () => {
    const summary = addressSummary([
      { address: '192.168.100.1', family: 'ipv4', side: 'lan', cidr: '192.168.100.0/24' },
      { address: '192.168.1.4', family: 'ipv4', side: 'wan', cidr: '192.168.1.0/24' },
      { address: '10.0.0.1', family: 'ipv4', side: 'dmz', cidr: '10.0.0.0/24' },
    ])
    expect(summary).toContain('+1')
  })

  it('shows both sides in the summary of a multi-homed node', () => {
    const summary = addressSummary([
      { address: '192.168.100.1', family: 'ipv4', side: 'lan', cidr: '192.168.100.0/24' },
      { address: '192.168.1.4', family: 'ipv4', side: 'wan', cidr: '192.168.1.0/24' },
    ])
    expect(summary).toContain('192.168.100.1')
    expect(summary).toContain('192.168.1.4')
    expect(summary).toContain('WAN')
  })

  it('falls back to the primary ip when no addresses are known', () => {
    expect(addressSummary(undefined, '192.0.2.9')).toBe('192.0.2.9')
  })
})

describe('groupDeviceAddresses', () => {
  const iface = (id: string, name: string, role: string) => ({
    id, device_id: 'dev-1', name, type: 'ethernet', role, admin_state: 'up', oper_state: 'up',
    carrier: true, speed_mbps: 1000, mtu: 1500, is_physical: true,
    rx_bytes: 0, tx_bytes: 0, rx_errors: 0, tx_errors: 0, rx_packets: 0, tx_packets: 0,
    rx_dropped: 0, tx_dropped: 0, last_seen: new Date().toISOString(),
  }) as never

  const addr = (over: Record<string, unknown>) => ({
    id: 'a', device_id: 'dev-1', address: '192.168.100.1', family: 'ipv4',
    source: 'openwrt', is_primary: false, first_seen: '', last_seen: '', ...over,
  }) as never

  it('resolves the side from the interface the address belongs to', () => {
    // The device endpoint returns flat rows without a role; the role must come
    // from the interface, otherwise LAN and WAN addresses are indistinguishable.
    const groups = groupDeviceAddresses(
      [
        addr({ address: '192.168.100.1', interface_id: 'if-lan', prefix_length: 24, is_primary: true }),
        addr({ address: '192.168.1.4', interface_id: 'if-wan', prefix_length: 24 }),
      ],
      [iface('if-lan', 'br-lan', 'lan'), iface('if-wan', 'eth0', 'wan')],
    )
    expect(groups).toHaveLength(2)
    expect(groups[0].side).toBe('lan')
    expect(groups[0].cidr).toBe('192.168.100.0/24')
    expect(groups[1].side).toBe('wan')
    expect(groups[1].cidr).toBe('192.168.1.0/24')
  })

  it('groups IPv6 addresses that share a subnet', () => {
    const groups = groupDeviceAddresses(
      [
        addr({ address: '2001:db8:100::10', family: 'ipv6', prefix_length: 64, is_primary: true }),
        addr({ address: '2001:db8:100::11', family: 'ipv6', prefix_length: 64 }),
      ],
      [],
    )
    expect(groups).toHaveLength(1)
    expect(groups[0].cidr).toBe('2001:db8:100::/64')
    expect(groups[0].addresses).toHaveLength(2)
  })

  it('supports uncompressed IPv4-embedded IPv6 addresses', () => {
    const groups = groupDeviceAddresses(
      [
        addr({ address: '0:0:0:0:0:ffff:192.0.2.10', family: 'ipv6', prefix_length: 112 }),
        addr({ address: '0:0:0:0:0:ffff:192.0.2.11', family: 'ipv6', prefix_length: 112 }),
      ],
      [],
    )
    expect(groups).toHaveLength(1)
    expect(groups[0].cidr).toBe('::ffff:c000:0/112')
  })

  it('rejects empty IPv6 zones and non-canonical IPv4 octets', () => {
    const groups = groupDeviceAddresses([
      addr({ address: '2001:db8::1%', family: 'ipv6', prefix_length: 64 }),
      addr({ address: '2001:db8::192.000.002.001', family: 'ipv6', prefix_length: 112 }),
      addr({ address: '192.000.002.001', family: 'ipv4', prefix_length: 24 }),
    ], [])
    expect(groups.every((group) => group.cidr === '')).toBe(true)
  })

  it('omits IPv6 link-local addresses, including zoned and uppercase forms', () => {
    const groups = groupDeviceAddresses([
      addr({ address: 'fe80::1', family: 'ipv6', prefix_length: 64 }),
      addr({ address: 'FE80::2%eth0', family: 'ipv6', prefix_length: 64 }),
      addr({ address: '2001:db8::1', family: 'ipv6', prefix_length: 64 }),
    ], [])
    expect(groups).toHaveLength(1)
    expect(groups[0].addresses.map((entry) => entry.address)).toEqual(['2001:db8::1'])
  })

  it('treats a zero prefix as unknown', () => {
    const groups = groupDeviceAddresses([
      addr({ address: '192.0.2.10', prefix_length: 0 }),
      addr({ address: '2001:db8::10', family: 'ipv6', prefix_length: 0 }),
    ], [])
    expect(groups.every((group) => group.cidr === '')).toBe(true)
  })

  it('leaves a remote device address without a side', () => {
    const groups = groupDeviceAddresses([addr({ address: '192.0.2.20', prefix_length: 24 })], [])
    expect(groups).toHaveLength(1)
    expect(groups[0].side).toBe('unknown')
    expect(groups[0].cidr).toBe('192.0.2.0/24')
  })

  it('returns nothing for a device with no addresses', () => {
    expect(groupDeviceAddresses([], [])).toEqual([])
    expect(groupDeviceAddresses(undefined, undefined)).toEqual([])
  })
})
