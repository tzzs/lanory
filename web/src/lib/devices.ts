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

import type { DeviceType, DeviceState, EdgeType, EdgeStatus, DiscoverySource } from '@/types/api'
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
