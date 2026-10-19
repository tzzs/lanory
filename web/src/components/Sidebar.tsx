import { NavLink, useLocation } from 'react-router-dom'
import {
  LayoutDashboard,
  Network,
  MonitorSmartphone,
  Activity,
  History,
  Settings,
  type LucideIcon,
} from 'lucide-react'
import { t } from '@/i18n'
import { cn } from '@/lib/cn'
import { useUIStore } from '@/stores/ui'
import { Badge } from '@/components/ui'

interface NavItem {
  to: string
  labelKey: string
  icon: LucideIcon
}

const mainNav: NavItem[] = [
  { to: '/', labelKey: 'nav.overview', icon: LayoutDashboard },
  { to: '/topology', labelKey: 'nav.topology', icon: Network },
  { to: '/devices', labelKey: 'nav.devices', icon: MonitorSmartphone },
  { to: '/monitoring', labelKey: 'nav.monitoring', icon: Activity },
  { to: '/events', labelKey: 'nav.events', icon: History },
]

const systemNav: NavItem[] = [{ to: '/settings', labelKey: 'nav.settings', icon: Settings }]

function NavSection({ title, items }: { title?: string; items: NavItem[] }) {
  return (
    <div className="px-3">
      {title ? (
        <p className="px-2 pb-1 pt-4 text-[10px] font-semibold uppercase tracking-wider text-muted">
          {t(title)}
        </p>
      ) : null}
      <ul className="space-y-0.5">
        {items.map(({ to, labelKey, icon: Icon }) => (
          <li key={to}>
            <NavLink
              to={to}
              end={to === '/'}
              className={({ isActive }) =>
                cn(
                  'flex items-center gap-2.5 rounded-md px-2 py-2 text-sm transition-colors',
                  isActive
                    ? 'bg-accent/12 text-accent font-medium'
                    : 'text-muted hover:bg-surface-2 hover:text-fg',
                )
              }
            >
              <Icon size={16} strokeWidth={1.75} aria-hidden="true" />
              <span>{t(labelKey)}</span>
            </NavLink>
          </li>
        ))}
      </ul>
    </div>
  )
}

export function Sidebar() {
  const demoMode = useUIStore((s) => s.demoMode)
  const realtimeConnected = useUIStore((s) => s.realtimeConnected)

  return (
    <aside className="flex h-full w-56 shrink-0 flex-col border-r border-border bg-surface">
      <div className="flex items-center gap-2.5 px-4 py-4">
        <div className="flex h-8 w-8 items-center justify-center rounded-md bg-accent text-white">
          <Network size={17} strokeWidth={2} aria-hidden="true" />
        </div>
        <div className="min-w-0">
          <p className="truncate text-sm font-semibold text-fg">{t('app.name')}</p>
          <p className="truncate text-[11px] text-muted">{t('app.slogan')}</p>
        </div>
      </div>

      <nav className="flex-1 overflow-y-auto pb-4" aria-label={t('nav.section_main')}>
        <NavSection items={mainNav} />
        <NavSection title="nav.section_system" items={systemNav} />
      </nav>

      <div className="border-t border-border px-3 py-3 space-y-2">
        {demoMode ? <Badge tone="warning">{t('app.demo_badge')}</Badge> : null}
        <div className="flex items-center gap-2 text-[11px] text-muted">
          <span
            className={cn(
              'inline-block h-1.5 w-1.5 rounded-full',
              realtimeConnected ? 'bg-success' : 'bg-muted',
            )}
            aria-hidden="true"
          />
          <span>{realtimeConnected ? t('state.online') : t('state.offline')}</span>
        </div>
      </div>
    </aside>
  )
}

export function TopBar() {
  const location = useLocation()
  const title =
    [...mainNav, ...systemNav].find((item) =>
      item.to === '/' ? location.pathname === '/' : location.pathname.startsWith(item.to),
    )?.labelKey ?? 'app.name'

  return (
    <header className="flex h-14 shrink-0 items-center justify-between border-b border-border bg-surface px-6">
      <h1 className="text-base font-semibold text-fg">{t(title)}</h1>
      <div className="text-xs text-muted">{t('app.slogan')}</div>
    </header>
  )
}
