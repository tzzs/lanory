import { describe, expect, it } from 'vitest'
import { formatSpeed, formatBytes, formatPercent, formatLatency, confidenceBand, formatConfidence, formatEdgeLabel, formatRelativeTime, formatMac, formatDuration } from '@/lib/devices'

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
