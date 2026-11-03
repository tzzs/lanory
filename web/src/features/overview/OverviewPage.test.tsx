import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { OverviewPage } from './OverviewPage'
import { t } from '@/i18n'

const { overviewMock, monitoringMock, topologyMock, discoveryMock, startScanMock } = vi.hoisted(() => ({
  overviewMock: vi.fn(), monitoringMock: vi.fn(), topologyMock: vi.fn(), discoveryMock: vi.fn(), startScanMock: vi.fn(),
}))
vi.mock('@/hooks/useApi', () => ({
  useOverview: () => overviewMock(),
  useMonitoring: () => monitoringMock(),
  useTopology: () => topologyMock(),
  useDiscoveryStatus: () => discoveryMock(),
  useStartScan: () => ({ mutate: startScanMock, isPending: false }),
}))

function Location() {
  const location = useLocation()
  return <output data-testid="location">{location.pathname}{location.search}</output>
}

function renderPage() {
  const client = new QueryClient()
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/']}>
        <Routes>
          <Route path="/" element={<OverviewPage />} />
          <Route path="/devices" element={<Location />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  overviewMock.mockReturnValue({ data: {
    counts: { total: 1, online: 1, offline: 0, unknown: 0, manual: 0 },
    interfaces: [], networks: [], recent_new_devices: [], recent_offline_devices: [], recent_events: [], anomalies: [],
    last_scan_at: '', discovery_enabled: true, demo_mode: false, generated_at: '',
  }, isLoading: false, refetch: vi.fn() })
  monitoringMock.mockReturnValue({ data: { interfaces: [] } })
  topologyMock.mockReturnValue({ data: { nodes: [], edges: [] }, isError: false })
  discoveryMock.mockReturnValue({ data: { running: false, enabled: true, collectors: [], interval: '10m' } })
  startScanMock.mockReset()
})

describe('overview navigation and discovery', () => {
  it('makes device count tiles link to the matching device filter', async () => {
    renderPage()
    const online = screen.getByRole('link', { name: new RegExp(t('overview.online')) })
    expect(online.getAttribute('href')).toBe('/devices?state=online')
    fireEvent.click(online)
    await waitFor(() => expect(screen.getByTestId('location').textContent).toBe('/devices?state=online'))
  })

  it('starts a discovery run when the manual scan button is pressed', () => {
    renderPage()
    fireEvent.click(screen.getByRole('button', { name: t('discovery.scan') }))
    expect(startScanMock).toHaveBeenCalledWith({}, expect.objectContaining({ onSuccess: expect.any(Function) }))
  })

  it('shows the latest completed scan result', () => {
    discoveryMock.mockReturnValue({ data: {
      running: false,
      enabled: true,
      collectors: [],
      interval: '10m',
      last_job: { status: 'completed', devices_found: 4, devices_new: 2 },
    } })
    renderPage()
    expect(screen.getByRole('status').textContent).toContain('4')
    expect(screen.getByRole('status').textContent).toContain('2')
  })
})
