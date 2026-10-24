import { useCallback, useEffect, useState } from 'react'
import { BrowserRouter } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { initTheme } from '@/lib/theme'
import { useUIStore } from '@/stores/ui'
import { useHealth } from '@/hooks/useApi'
import { useRealtime, type RealtimeEvent } from '@/hooks/useRealtime'
import { Navigation } from '@/components/Navigation'
import { isEmbedded } from '@/lib/embed'
import { t } from '@/i18n'
import { AlertBanner } from '@/components/AlertBanner'
import { TokenPrompt, useTokenGate } from '@/components/TokenPrompt'

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
  const [visible, setVisible] = useState(!document.hidden)
  useEffect(() => {
    const update = () => setVisible(!document.hidden)
    document.addEventListener('visibilitychange', update)
    return () => document.removeEventListener('visibilitychange', update)
  }, [])
  useRealtime({
    enabled: visible,
    onEvent: invalidate,
    onConnectionChange: setRealtimeConnected,
  })
  return null
}

export function AppShell({ children }: { children: React.ReactNode }) {
  useEffect(() => initTheme(), [])

  // The API requires a bearer token. When it rejects the current one the app
  // shows the token prompt instead of an empty page, so a first-time visitor
  // knows what to do rather than staring at a blank screen.
  const { required, checked, recheck } = useTokenGate()
  const embedded = isEmbedded()

  return (
    <BrowserRouter basename={embedded ? '/embed' : undefined}>
      <div className="flex h-full min-w-0">
        <div className="flex min-w-0 flex-1 flex-col">
          {required ? null : <Navigation embedded={embedded} />}
          <AlertBanner />
          <main className="min-h-0 flex-1 overflow-auto">
            {!checked ? <div className="p-6 text-sm text-muted" role="status">{t('app.loading')}</div> : required ? (
              <TokenPrompt onAuthenticated={() => void recheck()} />
            ) : (
              children
            )}
          </main>
        </div>
      </div>
      {!checked || required ? null : <DemoModeSync />}
      {!checked || required ? null : <RealtimeSync />}
    </BrowserRouter>
  )
}
