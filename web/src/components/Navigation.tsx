import { useEffect } from 'react'
import { NavLink, useLocation } from 'react-router-dom'
import { LayoutDashboard, Network, MonitorSmartphone, Activity, History, Settings } from 'lucide-react'
import { t } from '@/i18n'
import { cn } from '@/lib/cn'
import { useUIStore } from '@/stores/ui'
import { Badge } from '@/components/ui'
import { notifyParentRoute } from '@/lib/embed'

const items = [
  { to: '/', key: 'nav.overview', icon: LayoutDashboard },
  { to: '/topology', key: 'nav.topology', icon: Network },
  { to: '/devices', key: 'nav.devices', icon: MonitorSmartphone },
  { to: '/monitoring', key: 'nav.monitoring', icon: Activity },
  { to: '/events', key: 'nav.events', icon: History },
  { to: '/settings', key: 'nav.settings', icon: Settings },
]

/** The shared app navigation uses no horizontal space beside the content. */
export function Navigation({ embedded }: { embedded: boolean }) {
  const connected = useUIStore(s => s.realtimeConnected)
  const demo = useUIStore(s => s.demoMode)
  useUIStore(s => s.language)
  const location = useLocation()
  useEffect(() => notifyParentRoute(location.pathname + location.search), [location.pathname, location.search])

  return (
    <header className="shrink-0 border-b border-border bg-surface">
      {embedded ? null : (
        <div className="flex items-center justify-between gap-4 px-4 py-3 sm:px-6">
          <div className="flex items-center gap-2.5">
            <Network size={23} className="text-accent" aria-hidden="true" />
            <h1 className="text-base font-semibold">{t('app.name')}</h1>
          </div>
          <span className="hidden text-xs text-muted sm:block">{t('app.slogan')}</span>
        </div>
      )}
      <div className="flex min-w-0 items-center gap-3 px-3 sm:px-5">
        <nav className="min-w-0 flex-1 overflow-x-auto" aria-label={t('nav.section_main')}>
          <ul className="flex w-max gap-1">
            {items.map(({ to, key, icon: Icon }) => (
              <li key={to}>
                <NavLink to={to} end={to === '/'} className={({ isActive }) => cn(
                  'flex items-center gap-2 whitespace-nowrap border-b-2 px-3 py-3 text-sm transition-colors',
                  isActive ? 'border-accent font-medium text-accent' : 'border-transparent text-muted hover:text-fg',
                )}>
                  <Icon size={15} aria-hidden="true" />{t(key)}
                </NavLink>
              </li>
            ))}
          </ul>
        </nav>
        <div className="flex shrink-0 items-center gap-2 text-xs text-muted" role="status">
          {demo ? <Badge tone="warning">{t('app.demo_badge')}</Badge> : null}
          <span className={cn('h-1.5 w-1.5 rounded-full', connected ? 'bg-success' : 'bg-muted')} aria-hidden="true" />
          <span className="hidden sm:inline">{connected ? t('state.online') : t('state.offline')}</span>
        </div>
      </div>
    </header>
  )
}
