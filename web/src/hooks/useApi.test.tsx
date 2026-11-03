import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import { POLL, queryKeys, useAutoLayout, useCreateDevice, useCreateEdge, useCreateProbe, useDeleteDevice, useDeleteEdge, useDevices, useDiscoveryStatus, useEvents, useHealth, useMonitoring, useOverview, usePatchDevice, usePatchEdge, usePatchProbe, usePatchSettings, useProbes, useRunProbe, useSaveLayout, useSettings, useStartScan, useTopology, useTopologyEvidence, useRealtimeInvalidation, useDevice } from '@/hooks/useApi'

const { apiMock } = vi.hoisted(() => {
  const apiMock = new Proxy<Record<string, ReturnType<typeof vi.fn>>>({}, {
    get: (target, key: string) => target[key] ??= vi.fn().mockResolvedValue({ method: key }),
  })
  return { apiMock }
})
vi.mock('@/lib/api', () => ({ api: apiMock }))

function setup() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const wrapper = ({ children }: { children: React.ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>
  return { client, wrapper }
}

describe('API query hooks', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    for (const method of Object.values(apiMock)) method.mockResolvedValue({ method: 'mocked' })
  })
  afterEach(() => vi.restoreAllMocks())

  it('uses the health defaults and lets callers override query options', async () => {
    const { wrapper } = setup()
    const { result } = renderHook(() => useHealth({ staleTime: 1, refetchInterval: false }), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(api.health).toHaveBeenCalledOnce()
    expect(result.current.data).toEqual({ method: 'health' })
  })

  it('disables a missing device query and enables it once an id is supplied', async () => {
    const { wrapper } = setup()
    const { result, rerender } = renderHook(({ id }: { id: string | null }) => useDevice(id), { initialProps: { id: null as string | null }, wrapper })
    expect(result.current.fetchStatus).toBe('idle')
    expect(api.device).not.toHaveBeenCalled()
    rerender({ id: 'device-7' })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(api.device).toHaveBeenCalledWith('device-7')
    expect(result.current.data).toEqual({ method: 'device' })
  })

  it('toggles discovery polling with the enabled option', async () => {
    const { wrapper } = setup()
    const { result, rerender } = renderHook(({ enabled }: { enabled: boolean }) => useDiscoveryStatus(enabled), { initialProps: { enabled: false }, wrapper })
    expect(result.current.fetchStatus).toBe('idle')
    rerender({ enabled: true })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(api.discoveryStatus).toHaveBeenCalledOnce()
    expect(POLL.discovery).toBe(3000)
  })

  it('invalidates related caches after a successful mutation', async () => {
    const { client, wrapper } = setup()
    const invalidate = vi.spyOn(client, 'invalidateQueries').mockResolvedValue()
    const { result } = renderHook(() => useCreateDevice(), { wrapper })
    await act(async () => result.current.mutateAsync({ name: 'New device' } as never))
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['devices'] })
    expect(invalidate).toHaveBeenCalledWith({ queryKey: queryKeys.topology })
    expect(invalidate).toHaveBeenCalledWith({ queryKey: queryKeys.overview })
    expect(api.createDevice).toHaveBeenCalledOnce()
  })

  it('invalidates topology, monitoring, discovery and device data for realtime events', () => {
    const { client, wrapper } = setup()
    const invalidate = vi.spyOn(client, 'invalidateQueries').mockResolvedValue()
    const { result } = renderHook(() => useRealtimeInvalidation(), { wrapper })

    act(() => result.current({ type: 'device.offline' }))
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['devices'] })
    expect(invalidate).toHaveBeenCalledWith({ queryKey: queryKeys.overview })
    invalidate.mockClear()
    act(() => result.current({ type: 'link.updated' }))
    expect(invalidate).toHaveBeenCalledWith({ queryKey: queryKeys.topology })
    invalidate.mockClear()
    act(() => result.current({ type: 'probe.completed' }))
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['monitoring'] })
    invalidate.mockClear()
    act(() => result.current({ type: 'discovery.completed' }))
    expect(invalidate).toHaveBeenCalledWith({ queryKey: queryKeys.discovery })
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['devices'] })
    expect(invalidate).toHaveBeenCalledWith({ queryKey: queryKeys.topology })
    invalidate.mockClear()
    act(() => result.current({ type: 'unknown.event' }))
    expect(invalidate).not.toHaveBeenCalled()
  })

  it('loads each server query with its query key and parameters', async () => {
    const { wrapper, client } = setup()
    const queries = [
      useOverview, useTopology, useTopologyEvidence, useMonitoring, useProbes, useSettings,
    ]
    for (const useQueryHook of queries) {
      const { result, unmount } = renderHook(() => useQueryHook(), { wrapper })
      await waitFor(() => expect(result.current.isSuccess).toBe(true))
      unmount()
    }
    const { result: devices, unmount: unmountDevices } = renderHook(() => useDevices({ query: 'router' }), { wrapper })
    await waitFor(() => expect(devices.current.isSuccess).toBe(true))
    unmountDevices()
    const { result: events, unmount: unmountEvents } = renderHook(() => useEvents({ limit: 5 }), { wrapper })
    await waitFor(() => expect(events.current.isSuccess).toBe(true))
    unmountEvents()
    expect(client.getQueryCache().getAll().map((query) => query.queryKey)).toContainEqual(queryKeys.devices({ query: 'router' }))
    expect(api.devices).toHaveBeenCalledWith({ query: 'router' })
    expect(api.events).toHaveBeenCalledWith({ limit: 5 })
  })

  it('invalidates caches for edge, probe, scan and settings mutations', async () => {
    const { client, wrapper } = setup()
    const invalidate = vi.spyOn(client, 'invalidateQueries').mockResolvedValue()
    const cases = [
      [usePatchDevice, { id: 'd1', body: {} }, [['devices'], ['topology'], ['overview']], api.patchDevice],
      [useCreateEdge, {}, [['topology']], api.createEdge],
      [usePatchEdge, { id: 'e1', body: {} }, [['topology']], api.patchEdge],
      [useDeleteEdge, 'e1', [['topology']], api.deleteEdge],
      [useSaveLayout, {}, [['topology']], api.saveLayout],
      [useCreateProbe, {}, [['monitoring']], api.createProbe],
      [usePatchProbe, { id: 'p1', body: {} }, [['monitoring']], api.patchProbe],
      [useRunProbe, 'p1', [['monitoring']], api.runProbe],
      [usePatchSettings, {}, [['settings'], ['discovery', 'status']], api.patchSettings],
      [useStartScan, {}, [['discovery', 'status'], ['devices'], ['topology'], ['overview']], api.startScan],
    ] as const
    for (const [useMutationHook, input, expectedKeys, apiMethod] of cases) {
      invalidate.mockClear()
      const { result, unmount } = renderHook(() => useMutationHook(), { wrapper })
      await act(async () => result.current.mutateAsync(input as never))
      expect(apiMethod).toHaveBeenCalled()
      for (const key of expectedKeys) expect(invalidate).toHaveBeenCalledWith({ queryKey: key })
      unmount()
    }
  })

  it('supports edge auto-layout and does not invalidate cache before a result is requested', async () => {
    const { wrapper, client } = setup()
    const invalidate = vi.spyOn(client, 'invalidateQueries').mockResolvedValue()
    const { result } = renderHook(() => useAutoLayout(), { wrapper })
    await act(async () => result.current.mutateAsync({} as never))
    expect(api.autoLayout).toHaveBeenCalledOnce()
    expect(invalidate).not.toHaveBeenCalled()
  })

  it('invalidates the same server state after deleting a device', async () => {
    const { client, wrapper } = setup()
    const invalidate = vi.spyOn(client, 'invalidateQueries').mockResolvedValue()
    const { result } = renderHook(() => useDeleteDevice(), { wrapper })
    await act(async () => result.current.mutateAsync({ id: 'd1', mode: 'delete' }))
    expect(api.deleteDevice).toHaveBeenCalledWith('d1', 'delete')
    expect(invalidate).toHaveBeenCalledTimes(3)
  })
})
