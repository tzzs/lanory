/**
 * Device presentation helpers: icon mapping, type metadata and label lookups.
 *
 * These live in one place so the topology graph, the device table and the
 * detail panel all render a device identically.
 */

import {
  Globe,
  Router,
  Network,
  Wifi,
  Monitor,
  Laptop,
  Smartphone,
  Server,
  HardDrive,
  type LucideIcon,
  Cable,
  Flame,
  Radio,
  Boxes,
  Tablet,
  Watch,
  Tv,
  Cpu,
  Printer,
  Video,
  Container,
  Box,
  HelpCircle,
  CircleDashed,
} from 'lucide-react'

import type { DeviceType, DeviceState, EdgeType, EdgeStatus, DiscoverySource, InterfaceRole, NodeAddress, DeviceAddress, NetworkInterface } from '@/types/api'
import { t } from '@/i18n'

/** Icon per device type, matching the spec's requested mapping. */
export const DEVICE_ICONS: Record<DeviceType, LucideIcon> = {
  internet: Globe,
  modem_ont: Cable,
  router: Router,
  firewall: Flame,
  switch: Network,
  wireless_ap: Wifi,
  mesh_node: Radio,
  nas: HardDrive,
  server: Server,
  desktop: Monitor,
  laptop: Laptop,
  smartphone: Smartphone,
  tablet: Tablet,
  smartwatch: Watch,
  smart_tv: Tv,
  iot: Cpu,
  printer: Printer,
  camera: Video,
  vm: Boxes,
  container: Container,
  network_fiber: Cable,
  generic_node: Box,
  unknown: HelpCircle,
}

/** Icon for a synthetic topology node kind (network or unresolved segment). */
export const NODE_ICONS = {
  network: Network,
  unresolved: CircleDashed,
} as const

export function deviceIcon(type: DeviceType | undefined): LucideIcon {
  if (!type) return DEVICE_ICONS.unknown
  return DEVICE_ICONS[type] ?? DEVICE_ICONS.unknown
}

/** Colour token per device state. Used for dots, borders and badges. */
export const STATE_COLORS: Record<DeviceState, string> = {
  online: 'text-success',
  offline: 'text-danger',
  sleeping: 'text-warning',
  unknown: 'text-muted',
}

/** Background colour token per device state, for solid dots. */
export const STATE_DOT_COLORS: Record<DeviceState, string> = {
  online: 'bg-success',
  offline: 'bg-danger',
  sleeping: 'bg-warning',
  unknown: 'bg-muted',
}

/** Border colour per edge status so line state is not conveyed by colour alone. */
export const EDGE_STATUS_COLORS: Record<EdgeStatus, string> = {
  link_up: 'var(--success)',
  link_down: 'var(--danger)',
  reachable: 'var(--success)',
  unreachable: 'var(--danger)',
  unverified: 'var(--muted)',
}

export function deviceTypeLabel(type: DeviceType | undefined): string {
  if (!type) return t('type.unknown')
  return t(`type.${type}`)
}

export function deviceStateLabel(state: DeviceState | undefined): string {
  if (!state) return t('state.unknown')
  return t(`state.${state}`)
}

export function edgeTypeLabel(type: EdgeType | undefined): string {
  if (!type) return t('app.unknown')
  return t(`edge.${type}`)
}

export function sourceLabel(source: DiscoverySource | undefined): string {
  if (!source) return t('app.unknown')
  return t(`source.${source}`)
}

/** All device types in the order used by filter dropdowns. */
export const DEVICE_TYPE_OPTIONS: DeviceType[] = [
  'internet', 'modem_ont', 'router', 'firewall', 'switch', 'wireless_ap', 'mesh_node',
  'nas', 'server', 'desktop', 'laptop', 'smartphone', 'tablet', 'smartwatch', 'smart_tv',
  'iot', 'printer', 'camera', 'vm', 'container', 'network_fiber', 'generic_node', 'unknown',
]

export const DEVICE_STATE_OPTIONS: DeviceState[] = ['online', 'offline', 'sleeping', 'unknown']

/**
 * Formats a link speed for display.
 * 0 means "no negotiated speed" and renders as an em dash rather than 0 Mbps.
 */
export function formatSpeed(mbps: number | undefined): string {
  if (!mbps || mbps <= 0) return '—'
  if (mbps >= 1000) {
    const gbps = mbps / 1000
    return `${Number.isInteger(gbps) ? gbps : gbps.toFixed(1)} Gbps`
  }
  return `${mbps} Mbps`
}

/** Formats a byte counter as a compact human readable string. */
export function formatBytes(bytes: number | undefined): string {
  if (!bytes || bytes < 0) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  const rounded = value >= 100 || unit === 0 ? Math.round(value) : Math.round(value * 10) / 10
  return `${rounded} ${units[unit]}`
}

/** Formats a latency in milliseconds. */
export function formatLatency(ms: number | undefined): string {
  if (ms === undefined || ms === null) return '—'
  if (ms <= 0) return '—'
  if (ms < 1) return '<1 ms'
  return `${Math.round(ms)} ms`
}

/** Formats a percentage. */
export function formatPercent(pct: number | undefined): string {
  if (pct === undefined || pct === null) return '—'
  if (pct === 0) return '0%'
  if (pct < 0.1) return '<0.1%'
  return `${Math.round(pct * 10) / 10}%`
}

/** Formats a duration in seconds as a compact human readable string. */
export function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return '—'
  if (seconds < 60) return `${Math.round(seconds)}s`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ${Math.round(seconds % 60)}s`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ${minutes % 60}m`
  const days = Math.floor(hours / 24)
  return `${days}d ${hours % 24}h`
}

/** Relative time from an ISO timestamp, e.g. "3 minutes ago". */
export function formatRelativeTime(iso: string | undefined, locale = getLocale()): string {
  // Go serializes an unset time.Time as year 0001, not an empty string.
  if (!iso || iso.startsWith('0001-01-01T00:00:00')) return '—'
  const then = new Date(iso).getTime()
  if (Number.isNaN(then)) return '—'
  const diffMs = then - Date.now()
  const abs = Math.abs(diffMs)
  const rtf = new Intl.RelativeTimeFormat(locale, { numeric: 'auto' })
  if (abs < 45_000) return rtf.format(Math.round(diffMs / 1000), 'second')
  if (abs < 3_600_000) return rtf.format(Math.round(diffMs / 60_000), 'minute')
  if (abs < 86_400_000) return rtf.format(Math.round(diffMs / 3_600_000), 'hour')
  if (abs < 2_592_000_000) return rtf.format(Math.round(diffMs / 86_400_000), 'day')
  return rtf.format(Math.round(diffMs / 2_592_000_000), 'month')
}

function getLocale(): string {
  return document.documentElement.lang === 'zh-CN' ? 'zh-CN' : 'en'
}

/** Absolute timestamp for tooltips and detail panels. */
export function formatTimestamp(iso: string | undefined): string {
  if (!iso) return '—'
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return '—'
  return date.toLocaleString(getLocale(), { hour12: false })
}

/** Formats a MAC address in upper case with colons for display. */
export function formatMac(mac: string | undefined): string {
  if (!mac) return '—'
  return mac.toUpperCase()
}

/**
 * Builds the label shown on a topology edge, e.g. "eth2 · 2.5 Gbps".
 * The port name is the primary signal; the speed is secondary.
 */
export function formatEdgeLabel(parts: {
  port?: string
  speedMbps?: number
  label?: string
}): string {
  const { port, speedMbps, label } = parts
  const pieces: string[] = []
  if (label) pieces.push(label)
  if (port) pieces.push(port)
  const speed = formatSpeed(speedMbps)
  if (speed !== '—') pieces.push(speed)
  return pieces.join(' · ')
}

/** Confidence rendered as a percentage, with an explicit band for the UI. */
export function confidenceBand(confidence: number): 'high' | 'medium' | 'low' {
  if (confidence >= 0.8) return 'high'
  if (confidence >= 0.5) return 'medium'
  return 'low'
}

export function formatConfidence(confidence: number): string {
  if (!Number.isFinite(confidence)) return '—'
  return `${Math.round(Math.max(0, Math.min(1, confidence)) * 100)}%`
}

/**
 * One group of a node's addresses that share a subnet.
 *
 * A router is the case this exists for: 192.168.100.1/24 and 192.168.1.4/24 are
 * the same box, but they are two different networks, and listing them as one
 * comma-separated string hides that. Grouping by CIDR (and side) makes the
 * multi-homing explicit without inventing a second node.
 */
export interface AddressGroup {
  /** The subnet the addresses belong to, or "" when the prefix is unknown. */
  cidr: string
  /** The side of the box these addresses sit on, when it is a local address. */
  side: InterfaceRole
  /** The addresses in this group, primary first. */
  addresses: NodeAddress[]
}

/** Orders sides so the LAN comes before the WAN, matching the graph's flow. */
const SIDE_ORDER: Record<InterfaceRole, number> = {
  lan: 0,
  dmz: 1,
  wan: 2,
  virtual: 3,
  loopback: 4,
  unknown: 5,
}

/**
 * Groups a node's addresses by subnet, primary address first within a group.
 *
 * Addresses with no known prefix each become their own group: they are real
 * addresses, but claiming they share a network would be a guess.
 */
export function groupAddresses(addresses: NodeAddress[] | undefined): AddressGroup[] {
  if (!addresses?.length) return []
  const byKey = new Map<string, AddressGroup>()
  for (const address of addresses) {
    // A group is (subnet, side): the same subnet seen from two sides of a box is
    // still two groups, because the side is what the user is trying to read.
    const key = `${address.cidr ?? ''}|${address.side ?? 'unknown'}|${address.cidr ? '' : address.address}`
    const existing = byKey.get(key)
    if (existing) {
      existing.addresses.push(address)
    } else {
      byKey.set(key, { cidr: address.cidr ?? '', side: address.side ?? 'unknown', addresses: [address] })
    }
  }
  const groups = [...byKey.values()]
  for (const group of groups) {
    group.addresses.sort((a, b) => Number(!!b.is_primary) - Number(!!a.is_primary) || a.address.localeCompare(b.address))
  }
  groups.sort((a, b) => (SIDE_ORDER[a.side] ?? 5) - (SIDE_ORDER[b.side] ?? 5) || a.cidr.localeCompare(b.cidr))
  return groups
}

/** True when a node carries addresses on more than one known subnet. */
export function isMultiHomed(addresses: NodeAddress[] | undefined): boolean {
  const subnets = new Set((addresses ?? []).map((a) => a.cidr).filter(Boolean))
  return subnets.size > 1
}

/**
 * The compact address summary shown on a topology node card.
 *
 * Multi-homed nodes show their sides ("WAN 192.168.1.4") so the card never
 * implies the box has one address when it has two.
 */
export function addressSummary(addresses: NodeAddress[] | undefined, fallback?: string): string {
  const groups = groupAddresses(addresses)
  if (groups.length === 0) return fallback ?? ''
  if (groups.length === 1) return groups[0].addresses[0].address
  const visible = groups.slice(0, 2).map((g) => {
    const label = g.side === 'unknown' ? '' : `${g.side.toUpperCase()} `
    return label + g.addresses[0].address
  })
  if (groups.length > visible.length) visible.push(`+${groups.length - visible.length}`)
  return visible.join(' · ')
}

/**
 * Groups the addresses of a device-detail payload by subnet and side.
 *
 * The device endpoint returns flat DeviceAddress rows, which do not carry the
 * interface role. It is resolved here from the interface list so the detail
 * panel can say "LAN 192.168.100.1 · WAN 192.168.1.4" instead of printing a
 * comma-separated list in which the two sides are indistinguishable.
 */
export function groupDeviceAddresses(
  addresses: DeviceAddress[] | undefined,
  interfaces: NetworkInterface[] | undefined,
): AddressGroup[] {
  if (!addresses?.length) return []
  const roleByIface = new Map<string, InterfaceRole>()
  for (const iface of interfaces ?? []) {
    roleByIface.set(iface.id, iface.role)
  }
  const mapped: NodeAddress[] = addresses
    .filter((a) => !isLinkLocalIPv6(a.address))
    .map((a) => ({
      address: a.address,
      family: a.family,
      prefix_length: a.prefix_length,
      cidr: subnetOfAddress(a.address, a.prefix_length),
      side: (a.interface_id && roleByIface.get(a.interface_id)) || 'unknown',
      is_primary: a.is_primary,
    }))
  return groupAddresses(mapped)
}

/** Mirrors Go's netip.PrefixFrom(addr, prefix).Masked().String() for IP addresses. */
function isLinkLocalIPv6(address: string): boolean {
  const zoneIndex = address.indexOf('%')
  const withoutZone = zoneIndex < 0 ? address : address.slice(0, zoneIndex)
  const parsed = parseIPv6(withoutZone)
  if (parsed === undefined) return false
  return (parsed >> 118n) === 0b1111111010n
}

function subnetOfAddress(address: string, prefix?: number): string | undefined {
  if (prefix === undefined || !Number.isInteger(prefix) || prefix <= 0) return undefined

  const ipv4 = parseIPv4(address)
  if (ipv4 !== undefined) {
    if (prefix > 32) return undefined
    const mask = prefix === 0 ? 0 : (0xffffffff << (32 - prefix)) >>> 0
    const network = (ipv4 & mask) >>> 0
    return `${(network >>> 24) & 0xff}.${(network >>> 16) & 0xff}.${(network >>> 8) & 0xff}.${network & 0xff}/${prefix}`
  }

  const ipv6 = parseIPv6(address)
  if (ipv6 === undefined || prefix > 128) return undefined
  const mask = prefix === 0 ? 0n : ((1n << 128n) - 1n) ^ ((1n << BigInt(128 - prefix)) - 1n)
  return `${formatIPv6(ipv6 & mask)}/${prefix}`
}

function parseIPv4(address: string): number | undefined {
  const octets = address.split('.')
  if (octets.length !== 4) return undefined
  const values = octets.map(Number)
  if (values.some((value, index) => {
    const octet = octets[index]
    return !/^\d+$/.test(octet) || (octet.length > 1 && octet.startsWith('0')) || !Number.isInteger(value) || value < 0 || value > 255
  })) return undefined
  return ((values[0] << 24) | (values[1] << 16) | (values[2] << 8) | values[3]) >>> 0
}

function parseIPv6(address: string): bigint | undefined {
  const zoneIndex = address.indexOf('%')
  const withoutZone = zoneIndex < 0 ? address : address.slice(0, zoneIndex)
  if (zoneIndex >= 0 && address.length === zoneIndex + 1) return undefined
  if (!withoutZone.includes(':')) return undefined

  const pieces = withoutZone.split('::')
  if (pieces.length > 2) return undefined
  const left = pieces[0] ? pieces[0].split(':') : []
  let right = pieces.length === 2 && pieces[1] ? pieces[1].split(':') : []
  const tail = right.length > 0 ? right : left
  const last = tail[tail.length - 1]
  if (last?.includes('.')) {
    const mapped = parseIPv4(last)
    if (mapped === undefined) return undefined
    const replacement = [(mapped >>> 16).toString(16), (mapped & 0xffff).toString(16)]
    if (right.length > 0) right = [...right.slice(0, -1), ...replacement]
    else left.splice(left.length - 1, 1, ...replacement)
  }

  const groups = [...left, ...right]
  if (groups.some((group) => !/^[0-9a-f]{1,4}$/i.test(group))) return undefined
  if (pieces.length === 1 ? groups.length !== 8 : groups.length >= 8) return undefined
  const expanded = pieces.length === 2 ? [...left, ...Array(8 - groups.length).fill('0'), ...right] : groups
  return expanded.reduce((value, group) => (value << 16n) | BigInt(parseInt(group, 16)), 0n)
}

function formatIPv6(value: bigint): string {
  const groups = Array.from({ length: 8 }, (_, index) => Number((value >> BigInt((7 - index) * 16)) & 0xffffn))
  let bestStart = -1
  let bestLength = 1
  for (let start = 0; start < groups.length;) {
    if (groups[start] !== 0) {
      start++
      continue
    }
    let end = start
    while (end < groups.length && groups[end] === 0) end++
    if (end - start > bestLength) {
      bestStart = start
      bestLength = end - start
    }
    start = end
  }
  const rendered = groups.map((group) => group.toString(16))
  if (bestStart < 0) return rendered.join(':')
  const before = rendered.slice(0, bestStart).join(':')
  const after = rendered.slice(bestStart + bestLength).join(':')
  return `${before}::${after}`
}
