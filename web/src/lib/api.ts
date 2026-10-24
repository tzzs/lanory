/**
 * API client for the Lanory REST API.
 *
 * All requests go to /api/v1 on the same origin. Errors are normalised into an
 * ApiError so callers can branch on the machine readable `code` instead of
 * parsing messages.
 */

import type {
  Device,
  DeviceList,
  DeviceDetail,
  Topology,
  TopologyEdge,
  TopologyLayout,
  TopologyEvidence,
  Overview,
  MonitoringStatus,
  DiscoveryStatus,
  DiscoveryJob,
  EventList,
  HealthResponse,
  NetworkEvent,
  ProbeTarget,
  ProbeResult,
  CollectorStatus,
  CreateDeviceRequest,
  PatchDeviceRequest,
  MergeDeviceRequest,
  CreateEdgeRequest,
  PatchEdgeRequest,
  SaveLayoutRequest,
  AutoLayoutRequest,
  ScanRequest,
  CreateProbeRequest,
  PatchProbeRequest,
  PatchSettingsRequest,
  ErrorResponse,
} from '@/types/api'

const BASE = '/api/v1'

export class ApiError extends Error {
  readonly status: number
  readonly code?: string
  readonly details?: Record<string, string>

  constructor(status: number, body: ErrorResponse) {
    super(body.error || `request failed with status ${status}`)
    this.name = 'ApiError'
    this.status = status
    this.code = body.code
    this.details = body.details
  }
}

/** Sent with every mutating request to satisfy the CSRF check. */
const CSRF_HEADER = 'X-Requested-With'

function authHeaders(): Record<string, string> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  // The token is stored by the login flow; absent in the default LAN-only setup.
  const token = readToken()
  if (token) headers.Authorization = `Bearer ${token}`
  return headers
}

const TOKEN_KEY = 'lanory.token'
let sessionToken: string | null = null

/** LuCI bootstrap credentials stay in memory and never replace standalone login. */
export function setSessionToken(token: string | null): void {
  sessionToken = token
}

export function readToken(): string | null {
  if (sessionToken !== null) return sessionToken
  try {
    return localStorage.getItem(TOKEN_KEY)
  } catch {
    return null
  }
}

export function writeToken(token: string | null): void {
  try {
    if (token) localStorage.setItem(TOKEN_KEY, token)
    else localStorage.removeItem(TOKEN_KEY)
  } catch {
    /* storage unavailable (private mode) - auth then relies on LAN exemption */
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const method = (init?.method ?? 'GET').toUpperCase()
  const mutating = method !== 'GET' && method !== 'HEAD'

  const headers: Record<string, string> = authHeaders()
  if (mutating) headers[CSRF_HEADER] = 'XMLHttpRequest'
  if (init?.headers) Object.assign(headers, init.headers)

  let res: Response
  try {
    res = await fetch(`${BASE}${path}`, { ...init, method, headers })
  } catch (cause) {
    throw new ApiError(0, { error: `network unreachable: ${String(cause)}`, code: 'network' })
  }

  if (res.status === 204) return undefined as T

  const text = await res.text()
  const body = text ? safeParse(text) : null

  if (!res.ok) {
    const err = (body ?? {}) as ErrorResponse
    throw new ApiError(res.status, { error: err.error ?? res.statusText, code: err.code, details: err.details })
  }
  return body as T
}

function safeParse(text: string): unknown {
  try {
    return JSON.parse(text)
  } catch {
    return null
  }
}

/** Builds a query string, dropping empty and nullish values. */
function qs<T extends object>(params: T = {} as T): string {
  const search = new URLSearchParams()
  for (const [key, value] of Object.entries(params as Record<string, unknown>)) {
    if (value === undefined || value === null || value === '') continue
    if (Array.isArray(value)) {
      if (value.length) search.set(key, value.join(','))
      continue
    }
    search.set(key, String(value))
  }
  const s = search.toString()
  return s ? `?${s}` : ''
}

export interface DeviceQuery {
  query?: string
  type?: string
  state?: string
  source?: string
  interface?: string
  new_since?: string
  sort?: string
  order?: 'asc' | 'desc'
  limit?: number
  offset?: number
}

export const api = {
  health: () => request<HealthResponse>('/health'),

  overview: () => request<Overview>('/overview'),

  // ---- devices ----
  devices: (query: DeviceQuery = {}) => request<DeviceList>('/devices' + qs(query)),
  device: (id: string) => request<DeviceDetail>(`/devices/${encodeURIComponent(id)}`),
  createDevice: (body: CreateDeviceRequest) =>
    request<Device>('/devices', { method: 'POST', body: JSON.stringify(body) }),
  patchDevice: (id: string, body: PatchDeviceRequest) =>
    request<Device>(`/devices/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(body) }),
  deleteDevice: (id: string, mode: 'hide' | 'delete' = 'hide') =>
    request<void>(`/devices/${encodeURIComponent(id)}` + qs({ mode }), { method: 'DELETE' }),
  mergeDevice: (id: string, body: MergeDeviceRequest) =>
    request<Device>(`/devices/${encodeURIComponent(id)}/merge`, { method: 'POST', body: JSON.stringify(body) }),

  // ---- topology ----
  topology: () => request<Topology>('/topology'),
  topologyEvidence: () => request<TopologyEvidence>('/topology/evidence'),
  createEdge: (body: CreateEdgeRequest) =>
    request<TopologyEdge>('/topology/edges', { method: 'POST', body: JSON.stringify(body) }),
  patchEdge: (id: string, body: PatchEdgeRequest) =>
    request<TopologyEdge>(`/topology/edges/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(body) }),
  deleteEdge: (id: string) =>
    request<void>(`/topology/edges/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  saveLayout: (body: SaveLayoutRequest) =>
    request<{ saved: number }>('/topology/layout', { method: 'POST', body: JSON.stringify(body) }),
  autoLayout: (body: AutoLayoutRequest = {}) =>
    request<{ layout: TopologyLayout[] }>('/topology/auto-layout', { method: 'POST', body: JSON.stringify(body) }),

  // ---- discovery ----
  discoveryStatus: () => request<DiscoveryStatus>('/discovery/status'),
  discoveryCollectors: () => request<{ collectors: CollectorStatus[] }>('/discovery/collectors'),
  startScan: (body: ScanRequest = {}) =>
    request<DiscoveryJob>('/discovery/scan', { method: 'POST', body: JSON.stringify(body) }),
  discoveryJob: (id: string) => request<DiscoveryJob>(`/discovery/jobs/${encodeURIComponent(id)}`),
  cancelDiscoveryJob: (id: string) =>
    request<{ cancelled: boolean }>(`/discovery/jobs/${encodeURIComponent(id)}/cancel`, { method: 'POST' }),

  // ---- monitoring ----
  monitoringStatus: () => request<MonitoringStatus>('/monitoring/status'),
  probes: () => request<{ probes: ProbeTarget[] }>('/monitoring/probes'),
  createProbe: (body: CreateProbeRequest) =>
    request<ProbeTarget>('/monitoring/probes', { method: 'POST', body: JSON.stringify(body) }),
  patchProbe: (id: string, body: PatchProbeRequest) =>
    request<ProbeTarget>(`/monitoring/probes/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(body) }),
  deleteProbe: (id: string) =>
    request<void>(`/monitoring/probes/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  runProbe: (id: string) =>
    request<ProbeResult>(`/monitoring/probes/${encodeURIComponent(id)}/run`, { method: 'POST' }),
  monitoringHistory: (query: { device_id?: string; probe_id?: string; since?: string; limit?: number } = {}) =>
    request<{ results: ProbeResult[] }>('/monitoring/history' + qs(query)),

  // ---- events ----
  events: (query: { type?: string; device_id?: string; since?: string; until?: string; query?: string; limit?: number; offset?: number } = {}) =>
    request<EventList>('/events' + qs(query)),
  event: (id: string) => request<NetworkEvent>(`/events/${encodeURIComponent(id)}`),

  // ---- settings ----
  settings: () => request<{ settings: Record<string, string> }>('/settings'),
  patchSettings: (body: PatchSettingsRequest) =>
    request<{ settings: Record<string, string> }>('/settings', { method: 'PATCH', body: JSON.stringify(body) }),
}

// Re-exported for convenience so callers do not need a second import.
export type { Device, DeviceDetail, Topology, Overview, NetworkEvent, TopologyEdge, TopologyLayout, CollectorStatus, ProbeTarget, ProbeResult, EventList } from '@/types/api'
