import { useCallback, useEffect } from 'react'
import { BrowserRouter } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { initTheme } from '@/lib/theme'
import { useUIStore } from '@/stores/ui'
import { useHealth } from '@/hooks/useApi'
import { useRealtime, type RealtimeEvent } from '@/hooks/useRealtime'
import { Sidebar, TopBar } from '@/components/Sidebar'
import { AlertBanner } from '@/components/AlertBanner'

/** Applies SSE events to the query cache. */
function useInvalidate() {
  const qc = useQueryClient()
  return useCallback((event: RealtimeEvent) => {
    switch (event.type) {
      case 'device.updated':
      case 'device.online':
      case 'device.offline':
        void qc.invalidateQueries({ queryKey: ['devices'] })
        void qc.invalidateQueries({ queryKey: ['overview'] })
        break
      case 'topology.updated':
      case 'link.updated':
        void qc.invalidateQueries({ queryKey: ['topology'] })
        void qc.invalidateQueries({ queryKey: ['overview'] })
        break
      case 'probe.completed':
        void qc.invalidateQueries({ queryKey: ['monitoring'] })
        break
      case 'discovery.started':
      case 'discovery.completed':
        void qc.invalidateQueries({ queryKey: ['discovery'] })
        void qc.invalidateQueries({ queryKey: ['devices'] })
        void qc.invalidateQueries({ queryKey: ['topology'] })
        break
      case 'settings.updated':
        void qc.invalidateQueries({ queryKey: ['settings'] })
        break
      default:
        break
    }
  }, [qc])
}

function DemoModeSync() {
  const setDemoMode = useUIStore((s) => s.setDemoMode)
  const health = useHealth()
  useEffect(() => {
    if (typeof health.data?.demo_mode === 'boolean') {
      setDemoMode(health.data.demo_mode)
    }
  }, [health.data?.demo_mode, setDemoMode])
  return null
}

function RealtimeSync() {
  const setRealtimeConnected = useUIStore((s) => s.setRealtimeConnected)
  const invalidate = useInvalidate()
  useRealtime({
    onEvent: invalidate,
    onConnectionChange: setRealtimeConnected,
  })
  return null
}

export function AppShell({ children }: { children: React.ReactNode }) {
  useEffect(() => initTheme(), [])

  return (
    <BrowserRouter>
      <div className="flex h-full">
        <Sidebar />
        <div className="flex min-w-0 flex-1 flex-col">
          <TopBar />
          <AlertBanner />
          <main className="min-h-0 flex-1 overflow-auto">{children}</main>
        </div>
      </div>
      <DemoModeSync />
      <RealtimeSync />
    </BrowserRouter>
  )
}
