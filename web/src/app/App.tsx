import { Suspense, lazy } from 'react'
import { Routes, Route, Navigate } from 'react-router-dom'
import { QueryClientProvider } from '@tanstack/react-query'
import { queryClient } from '@/lib/queryClient'
import { AppShell } from '@/app/AppShell'
import { OverviewPage } from '@/features/overview/OverviewPage'
import { DevicesPage } from '@/features/devices/DevicesPage'
import { MonitoringPage } from '@/features/monitoring/MonitoringPage'
import { EventsPage } from '@/features/events/EventsPage'
import { SettingsPage } from '@/features/settings/SettingsPage'
import { Skeleton } from '@/components/ui'

// The topology page pulls in React Flow (~100 kB); load it on demand.
const TopologyPage = lazy(() =>
  import('@/features/topology/TopologyPage').then((m) => ({ default: m.TopologyPage })),
)

function RouteFallback() {
  return (
    <div className="space-y-4 p-6">
      <Skeleton className="h-8 w-48" />
      <Skeleton className="h-64 w-full" />
    </div>
  )
}

export function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <AppShell>
        <Suspense fallback={<RouteFallback />}>
          <Routes>
            <Route path="/" element={<OverviewPage />} />
            <Route path="/topology" element={<TopologyPage />} />
            <Route path="/devices" element={<DevicesPage />} />
            <Route path="/devices/:id" element={<DevicesPage />} />
            <Route path="/monitoring" element={<MonitoringPage />} />
            <Route path="/events" element={<EventsPage />} />
            <Route path="/settings" element={<SettingsPage />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </Suspense>
      </AppShell>
    </QueryClientProvider>
  )
}
