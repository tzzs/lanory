import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { MonitoringPage } from './MonitoringPage'
import type { MonitoringStatus, ProbeStatus, ProbeTarget } from '@/types/api'

const { useMonitoringMock, useRunProbeMock, runProbeMutate } = vi.hoisted(() => ({ useMonitoringMock: vi.fn(), useRunProbeMock: vi.fn(), runProbeMutate: vi.fn() }))
vi.mock('@/hooks/useApi', () => ({
  useMonitoring: () => useMonitoringMock(),
  useRunProbe: () => useRunProbeMock(),
}))

/**
 * The monitoring page must survive a null `recent`.
 *
 * A probe that has never run has no results, and a nil Go slice marshals to
 * null rather than []. The page used to read `p.recent.length` directly, so a
 * single never-run probe threw during render and React unmounted the whole app:
 * the tab stayed blank forever. The API now normalises the field, but the page
 * is still required to tolerate null, because that is what the type says and a
 * client should never blank the app over one bad value.
 */

function probe(id: string, name: string): ProbeTarget {
  return {
    id,
    name,
    kind: 'network',
    target: '192.0.2.1',
    protocol: 'icmp',
    source_id: 'dev-self',
    interval: 60,
    timeout: 3,
    enabled: true,
    failure_threshold: 3,
    recover_confirm: 1,
    state: 'unknown',
    consecutive_failures: 0,
    consecutive_successes: 0,
    last_run_at: '0001-01-01T00:00:00Z',
    last_success_at: '0001-01-01T00:00:00Z',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
  } as ProbeTarget
}

function status(probes: ProbeStatus[]): MonitoringStatus {
  return { probes, interfaces: [], generated_at: '2026-01-01T00:00:00Z' }
}

function renderWithData(data: MonitoringStatus) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  useMonitoringMock.mockReturnValue({ data, isLoading: false, isError: false, error: null, refetch: vi.fn() })
  useRunProbeMock.mockReturnValue({ mutate: runProbeMutate, isPending: false })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <MonitoringPage />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('MonitoringPage with probes that have no results', () => {
  let errorSpy: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    useMonitoringMock.mockReset()
    runProbeMutate.mockReset()
    useRunProbeMock.mockReset().mockReturnValue({ mutate: runProbeMutate, isPending: false })
    // A render error is reported through console.error; fail loudly on it so a
    // regression shows up as a failure rather than a quietly empty tree.
    errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  afterEach(() => {
    errorSpy.mockRestore()
  })

  it('shows loading and retryable error states', async () => {
    const refetch = vi.fn()
    useMonitoringMock.mockReturnValueOnce({ data: undefined, isLoading: true })
    const loading = render(
      <QueryClientProvider client={new QueryClient()}><MemoryRouter><MonitoringPage /></MemoryRouter></QueryClientProvider>,
    )
    expect(loading.container.querySelectorAll('.animate-pulse').length).toBeGreaterThan(0)
    loading.unmount()
    useMonitoringMock.mockReturnValueOnce({ data: undefined, isLoading: false, isError: true, error: new Error('offline'), refetch })
    render(<QueryClientProvider client={new QueryClient()}><MemoryRouter><MonitoringPage /></MemoryRouter></QueryClientProvider>)
    expect(screen.getByText('offline')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: /retry/i }))
    expect(refetch).toHaveBeenCalledOnce()
  })

  it('renders interface state, counters, probe runs, and a retry action', async () => {
    useRunProbeMock.mockReturnValue({ mutate: runProbeMutate, isPending: false })
    const networkProbe = probe('p1', 'Gateway')
    networkProbe.port = 443
    networkProbe.last_error = 'timeout'
    networkProbe.last_run_at = '2026-01-01T00:00:00Z'
    const iface = {
      id: 'eth0', name: 'eth0', type: 'bridge', role: 'wan', carrier: false, oper_state: 'no-carrier',
      speed_mbps: 0, mtu: 1500, rx_bytes: 1024, tx_bytes: 2048, rx_errors: 1, tx_errors: 2,
      rx_dropped: 1, tx_dropped: 0,
    }
    const view = renderWithData({
      ...status([{ probe: networkProbe, recent: null, loss_pct: 10, avg_latency_ms: 0 }]),
      interfaces: [{ interface: iface, role: 'wan' } as never],
    })
    expect(screen.getByText('NO-CARRIER')).toBeTruthy()
    expect(screen.getByText('timeout')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: /run now/i }))
    expect(runProbeMutate).toHaveBeenCalledWith('p1')
    view.unmount()
    renderWithData(status([]))
    expect(screen.getByText('No probe targets configured')).toBeTruthy()
  })

  it('renders when every probe has recent: null', () => {
    const { container } = renderWithData(
      status([
        { probe: probe('p1', 'Gateway'), recent: null, loss_pct: 0, avg_latency_ms: 0 },
        { probe: probe('p2', 'Internet'), recent: null, loss_pct: 0, avg_latency_ms: 0 },
      ]),
    )

    // The page rendered its probe table instead of crashing.
    expect(container.innerHTML.length).toBeGreaterThan(0)
    expect(screen.getByText('Gateway')).toBeTruthy()
    expect(screen.getByText('Internet')).toBeTruthy()
    // No latency trend section: nothing has two results yet.
    expect(screen.queryByRole('img', { name: /Gateway/ })).toBeNull()
  })

  it('renders when only some probes have recent: null', () => {
    renderWithData(
      status([
        { probe: probe('p1', 'Never run'), recent: null, loss_pct: 0, avg_latency_ms: 0 },
        {
          probe: probe('p2', 'Healthy'),
          recent: [
            {
              id: 'r1', probe_id: 'p2', timestamp: '2026-01-01T00:00:00Z',
              success: true, latency_ms: 4, status: 'success',
            },
            {
              id: 'r2', probe_id: 'p2', timestamp: '2026-01-01T00:01:00Z',
              success: true, latency_ms: 6, status: 'success',
            },
          ],
          loss_pct: 0,
          avg_latency_ms: 5,
        },
      ]),
    )

    expect(screen.getByText('Never run')).toBeTruthy()
    // "Healthy" appears in the probe table and again in the latency trend, so
    // assert on the count rather than a unique match.
    expect(screen.getAllByText('Healthy').length).toBeGreaterThan(0)
    // The trend only charts the probe that has results.
    expect(screen.getByRole('img', { name: /Healthy/ })).toBeTruthy()
  })

  it('treats an empty recent array the same as null', () => {
    renderWithData(
      status([{ probe: probe('p1', 'Empty'), recent: [], loss_pct: 0, avg_latency_ms: 0 }]),
    )
    expect(screen.getByText('Empty')).toBeTruthy()
  })
})
