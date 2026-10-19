import { AlertTriangle } from 'lucide-react'
import { useUIStore } from '@/stores/ui'
import { t } from '@/i18n'

/**
 * Persistent banner shown whenever the service runs in demo mode, so simulated
 * data can never be mistaken for a real scan result.
 */
export function AlertBanner() {
  const demoMode = useUIStore((s) => s.demoMode)
  if (!demoMode) return null

  return (
    <div
      role="status"
      className="flex items-center gap-2 border-b border-warning/30 bg-warning/12 px-6 py-2 text-xs text-warning"
    >
      <AlertTriangle size={14} strokeWidth={2} aria-hidden="true" />
      <span>{t('app.demo_banner')}</span>
    </div>
  )
}
