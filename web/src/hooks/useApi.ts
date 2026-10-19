/**
 * TanStack Query hooks for the Lanory API.
 *
 * Polling intervals are conservative on purpose: Lanory runs on a router and
 * must not generate constant load. Real-time updates arrive over SSE, so the
 * polls are a safety net rather than the primary mechanism.
 */

import { useQuery, useMutation, useQueryClient, type UseQueryOptions } from '@tanstack/react-query'
import { api, type DeviceQuery } from '@/lib/api'
import type { Device, HealthResponse, ProbeTarget, TopologyEdge } from '@/types/api'
import type {
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
} from '@/types/api'

export const queryKeys = {
  health: ['health'] as const,
  overview: ['overview'] as const,
  topology: ['topology'] as const,
  topologyEvidence: ['topology', 'evidence'] as const,
  devices: (query: DeviceQuery) => ['devices', query] as const,
  device: (id: string) => ['devices', id] as const,
  monitoring: ['monitoring', 'status'] as const,
  probes: ['monitoring', 'probes'] as const,
  discovery: ['discovery', 'status'] as const,
  discoveryJob: (id: string) => ['discovery', 'job', id] as const,
  collectors: ['discovery', 'collectors'] as const,
  events: (params: Record<string, unknown>) => ['events', params] as const,
  settings: ['settings'] as const,
}

/** Interval constants (ms). Kept coarse to limit router load. */
export const POLL = {
  overview: 15_000,
  topology: 15_000,
  devices: 15_000,
  monitoring: 10_000,
  events: 20_000,
  discovery: 3_000,
} as const

export function useHealth(options?: Partial<UseQueryOptions<HealthResponse>>) {
  return useQuery({
    queryKey: queryKeys.health,
    queryFn: api.health,
    staleTime: 30_000,
    refetchInterval: 60_000,
    ...options,
  })
}

export function useOverview() {
  return useQuery({ queryKey: queryKeys.overview, queryFn: api.overview, refetchInterval: POLL.overview })
}

export function useTopology() {
  return useQuery({ queryKey: queryKeys.topology, queryFn: api.topology, refetchInterval: POLL.topology })
}

export function useTopologyEvidence() {
  return useQuery({ queryKey: queryKeys.topologyEvidence, queryFn: api.topologyEvidence, staleTime: 60_000 })
}

export function useDevices(query: DeviceQuery = {}) {
  return useQuery({
    queryKey: queryKeys.devices(query),
    queryFn: () => api.devices(query),
    refetchInterval: POLL.devices,
    placeholderData: (prev) => prev,
  })
}

export function useDevice(id: string | null) {
  return useQuery({
    queryKey: queryKeys.device(id ?? ''),
    queryFn: () => api.device(id as string),
    enabled: Boolean(id),
  })
}

export function useMonitoring() {
  return useQuery({ queryKey: queryKeys.monitoring, queryFn: api.monitoringStatus, refetchInterval: POLL.monitoring })
}

export function useProbes() {
  return useQuery({ queryKey: queryKeys.probes, queryFn: api.probes, staleTime: 30_000 })
}

export function useDiscoveryStatus(enabled = true) {
  return useQuery({
    queryKey: queryKeys.discovery,
    queryFn: api.discoveryStatus,
    refetchInterval: enabled ? POLL.discovery : false,
    enabled,
  })
}

export function useCollectors() {
  return useQuery({ queryKey: queryKeys.collectors, queryFn: api.discoveryCollectors, staleTime: 60_000 })
}

export function useEvents(params: { type?: string; device_id?: string; since?: string; limit?: number } = {}) {
  return useQuery({
    queryKey: queryKeys.events(params),
    queryFn: () => api.events(params),
    refetchInterval: POLL.events,
    placeholderData: (prev) => prev,
  })
}

export function useSettings() {
  return useQuery({ queryKey: queryKeys.settings, queryFn: api.settings, staleTime: 60_000 })
}

/* -------------------------------------------------------------------------- */
/* Mutations                                                                  */
/* -------------------------------------------------------------------------- */

export function useCreateDevice() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: CreateDeviceRequest) => api.createDevice(body),
    onSuccess: (device: Device) => {
      void qc.invalidateQueries({ queryKey: ['devices'] })
      void qc.invalidateQueries({ queryKey: queryKeys.topology })
      void qc.invalidateQueries({ queryKey: queryKeys.overview })
      return device
    },
  })
}

export function usePatchDevice() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: PatchDeviceRequest }) => api.patchDevice(id, body),
    onSuccess: (device: Device) => {
      void qc.invalidateQueries({ queryKey: ['devices'] })
      void qc.invalidateQueries({ queryKey: queryKeys.topology })
      void qc.invalidateQueries({ queryKey: queryKeys.overview })
      return device
    },
  })
}

export function useDeleteDevice() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, mode }: { id: string; mode?: 'hide' | 'delete' }) => api.deleteDevice(id, mode),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['devices'] })
      void qc.invalidateQueries({ queryKey: queryKeys.topology })
      void qc.invalidateQueries({ queryKey: queryKeys.overview })
    },
  })
}

export function useMergeDevice() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: MergeDeviceRequest }) => api.mergeDevice(id, body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['devices'] })
      void qc.invalidateQueries({ queryKey: queryKeys.topology })
      void qc.invalidateQueries({ queryKey: queryKeys.overview })
    },
  })
}

export function useCreateEdge() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: CreateEdgeRequest) => api.createEdge(body),
    onSuccess: (edge: TopologyEdge) => {
      void qc.invalidateQueries({ queryKey: queryKeys.topology })
      return edge
    },
  })
}

export function usePatchEdge() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: PatchEdgeRequest }) => api.patchEdge(id, body),
    onSuccess: (edge: TopologyEdge) => {
      void qc.invalidateQueries({ queryKey: queryKeys.topology })
      return edge
    },
  })
}

export function useDeleteEdge() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.deleteEdge(id),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: queryKeys.topology })
    },
  })
}

export function useSaveLayout() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: SaveLayoutRequest) => api.saveLayout(body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: queryKeys.topology })
    },
  })
}

export function useAutoLayout() {
  return useMutation({
    mutationFn: (body: AutoLayoutRequest) => api.autoLayout(body),
  })
}

export function useStartScan() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: ScanRequest) => api.startScan(body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: queryKeys.discovery })
      void qc.invalidateQueries({ queryKey: ['devices'] })
      void qc.invalidateQueries({ queryKey: queryKeys.topology })
      void qc.invalidateQueries({ queryKey: queryKeys.overview })
    },
  })
}

export function useCreateProbe() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: CreateProbeRequest) => api.createProbe(body),
    onSuccess: (probe: ProbeTarget) => {
      void qc.invalidateQueries({ queryKey: ['monitoring'] })
      return probe
    },
  })
}

export function usePatchProbe() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: PatchProbeRequest }) => api.patchProbe(id, body),
    onSuccess: (probe: ProbeTarget) => {
      void qc.invalidateQueries({ queryKey: ['monitoring'] })
      return probe
    },
  })
}

export function useRunProbe() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.runProbe(id),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['monitoring'] })
    },
  })
}

export function usePatchSettings() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: PatchSettingsRequest) => api.patchSettings(body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: queryKeys.settings })
      void qc.invalidateQueries({ queryKey: queryKeys.discovery })
    },
  })
}

/** Applies an SSE event to the query cache. */
export function useRealtimeInvalidation() {
  const qc = useQueryClient()
  return (event: { type: string }) => {
    switch (event.type) {
      case 'device.updated':
      case 'device.online':
      case 'device.offline':
        void qc.invalidateQueries({ queryKey: ['devices'] })
        void qc.invalidateQueries({ queryKey: queryKeys.overview })
        break
      case 'topology.updated':
      case 'link.updated':
        void qc.invalidateQueries({ queryKey: queryKeys.topology })
        void qc.invalidateQueries({ queryKey: queryKeys.overview })
        break
      case 'probe.completed':
        void qc.invalidateQueries({ queryKey: ['monitoring'] })
        break
      case 'discovery.started':
      case 'discovery.completed':
        void qc.invalidateQueries({ queryKey: queryKeys.discovery })
        void qc.invalidateQueries({ queryKey: ['devices'] })
        void qc.invalidateQueries({ queryKey: queryKeys.topology })
        break
      default:
        break
    }
  }
}
