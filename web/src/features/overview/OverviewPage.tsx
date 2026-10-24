import { Link } from 'react-router-dom'
import { NetworkSummary } from './NetworkSummary'
import {
  MonitorSmartphone,
  Wifi,
  WifiOff,
  HelpCircle,
} from 'lucide-react'
import { t } from '@/i18n'
import { useOverview, useMonitoring } from '@/hooks/useApi'
import {
  Card,
  CardHeader,
  CardTitle,
  CardBody,
  Skeleton,
  EmptyState,
  ErrorState,
  Button,
  Badge,
  StatusDot,
} from '@/components/ui'
import {
  deviceIcon,
  formatRelativeTime,
  formatSpeed,
  sourceLabel,
} from '@/lib/devices'
import type { Device, NetworkEvent, InterfaceStatus } from '@/types/api'

/* -------------------------------------------------------------------------- */
/* Stat tile                                                                  */
/* -------------------------------------------------------------------------- */

function StatTile({
  label,
  value,
  icon: Icon,
  tone = 'neutral',
  hint,
}: {
  label: string
  value: number | string
  icon: typeof Wifi
  tone?: 'neutral' | 'success' | 'danger' | 'muted'
  hint?: string
}) {
  const toneClass =
    tone === 'success'
      ? 'text-success'
      : tone === 'danger'
        ? 'text-danger'
        : tone === 'muted'
          ? 'text-muted'
          : 'text-fg'

  return (
    <Card className="p-4">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="text-xs text-muted">{label}</p>
          <p className={`mt-1 text-2xl font-semibold tabular-nums ${toneClass}`}>{value}</p>
          {hint ? <p className="mt-0.5 text-[11px] text-muted">{hint}</p> : null}
        </div>
        <Icon size={18} strokeWidth={1.75} className="shrink-0 text-muted" aria-hidden="true" />
      </div>
    </Card>
  )
}

/* -------------------------------------------------------------------------- */
/* Interface row                                                              */
/* -------------------------------------------------------------------------- */

function InterfaceRow({ status }: { status: InterfaceStatus }) {
  const iface = status.interface
  const up = iface.carrier && iface.oper_state !== 'down' && iface.oper_state !== 'no-carrier'

  return (
    <div className="flex items-center justify-between gap-3 py-2">
      <div className="flex min-w-0 items-center gap-2.5">
        <StatusDot state={up ? 'online' : 'offline'} />
        <span className="font-mono text-sm text-fg">{iface.name}</span>
        <Badge tone={status.role === 'wan' ? 'accent' : 'neutral'}>
          {status.role === 'wan' ? 'WAN' : 'LAN'}
        </Badge>
        {iface.type === 'bridge' ? <Badge>{t('type.switch')}</Badge> : null}
      </div>
      <div className="flex shrink-0 items-center gap-3 text-xs text-muted">
        <span className="tabular-nums">{formatSpeed(iface.speed_mbps)}</span>
        <span className="font-mono">{up ? 'UP' : 'NO-CARRIER'}</span>
      </div>
    </div>
  )
}

/* -------------------------------------------------------------------------- */
/* Device mini row                                                            */
/* -------------------------------------------------------------------------- */

function DeviceMiniRow({ device, trailing }: { device: Device; trailing?: React.ReactNode }) {
  const Icon = deviceIcon(device.type)
  return (
    <li className="flex items-center justify-between gap-3 py-2">
      <Link
        to={`/devices/${device.id}`}
        className="flex min-w-0 items-center gap-2.5 rounded hover:text-accent"
      >
        <Icon size={15} strokeWidth={1.75} className="shrink-0 text-muted" aria-hidden="true" />
        <span className="truncate text-sm text-fg">{device.name}</span>
        {device.is_manual ? <Badge tone="accent">{t('devices.manual_badge')}</Badge> : null}
      </Link>
      <div className="flex shrink-0 items-center gap-2 text-[11px] text-muted">
        {device.primary_ipv4 ? <span className="font-mono">{device.primary_ipv4}</span> : null}
        {trailing ?? <span>{formatRelativeTime(device.last_seen)}</span>}
      </div>
    </li>
  )
}

/* -------------------------------------------------------------------------- */
/* Event row                                                                  */
/* -------------------------------------------------------------------------- */

function EventMiniRow({ event }: { event: NetworkEvent }) {
  return (
    <li className="flex items-start justify-between gap-3 py-2">
      <div className="min-w-0">
        <p className="truncate text-sm text-fg">{event.message || t(`event.${event.event_type}`)}</p>
        <p className="text-[11px] text-muted">
          {t(`event.${event.event_type}`)}
          {event.old_value && event.new_value ? ` · ${event.old_value} → ${event.new_value}` : ''}
        </p>
      </div>
      <span className="shrink-0 text-[11px] text-muted">{formatRelativeTime(event.timestamp)}</span>
    </li>
  )
}

/* -------------------------------------------------------------------------- */
/* Mini topology (simplified tree)                                            */
/* -------------------------------------------------------------------------- */

export function OverviewPage() {
  const { data, isLoading, error, refetch } = useOverview()
  const monitoring = useMonitoring()

  if (isLoading) {
    return (
      <div className="space-y-4 p-6">
        <Skeleton className="h-8 w-40" />
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          {Array.from({ length: 4 }).map((_, i) => (
            <Skeleton key={i} className="h-24" />
          ))}
        </div>
        <Skeleton className="h-64" />
      </div>
    )
  }

  if (!data) {
    return (
      <ErrorState
        title={t('app.error')}
        description={error instanceof Error ? error.message : undefined}
        action={
          <Button variant="primary" onClick={() => void refetch()}>
            {t('app.retry')}
          </Button>
        }
      />
    )
  }

  const { counts } = data
  const connectionState =
    data.anomalies.length === 0 ? 'healthy' : data.anomalies.some((a) => a.severity === 'critical') ? 'down' : 'degraded'

  return (
    <div className="space-y-5 p-6">
      {/* stats */}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatTile label={t('overview.total_devices')} value={counts.total} icon={MonitorSmartphone} />
        <StatTile label={t('overview.online')} value={counts.online} icon={Wifi} tone="success" />
        <StatTile
          label={t('overview.offline')}
          value={counts.offline}
          icon={WifiOff}
          tone={counts.offline > 0 ? 'danger' : 'neutral'}
        />
        <StatTile label={t('overview.unknown')} value={counts.unknown} icon={HelpCircle} tone="muted" />
      </div>

      {/* mini topology */}
      <Card>
        <CardHeader>
          <CardTitle>{t('nav.topology')}</CardTitle>
          <span className="text-xs text-muted">
            {t('overview.last_scan')}:{' '}
            {formatRelativeTime(data.last_scan_at) === '—'
              ? t('overview.never_scanned')
              : formatRelativeTime(data.last_scan_at)}
          </span>
        </CardHeader>
        <CardBody className="p-0">
          <NetworkSummary overview={data} />
        </CardBody>
      </Card>

      {/* three columns */}
      <div className="grid gap-4 lg:grid-cols-3">
        <Card>
          <CardHeader>
            <CardTitle>{t('overview.recent_new')}</CardTitle>
          </CardHeader>
          <CardBody className="p-0 px-4 pb-2">
            {data.recent_new_devices.length === 0 ? (
              <EmptyState title={t('overview.no_devices')} className="py-8" />
            ) : (
              <ul className="divide-y divide-border">
                {data.recent_new_devices.slice(0, 6).map((device) => (
                  <DeviceMiniRow key={device.id} device={device} />
                ))}
              </ul>
            )}
          </CardBody>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>{t('overview.recent_offline')}</CardTitle>
          </CardHeader>
          <CardBody className="p-0 px-4 pb-2">
            {data.recent_offline_devices.length === 0 ? (
              <EmptyState title={t('overview.no_devices')} className="py-8" />
            ) : (
              <ul className="divide-y divide-border">
                {data.recent_offline_devices.slice(0, 6).map((device) => (
                  <DeviceMiniRow key={device.id} device={device} />
                ))}
              </ul>
            )}
          </CardBody>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>{t('overview.recent_events')}</CardTitle>
            <Badge tone={connectionState === 'healthy' ? 'success' : connectionState === 'down' ? 'danger' : 'warning'}>
              {t(`overview.${connectionState}`)}
            </Badge>
          </CardHeader>
          <CardBody className="p-0 px-4 pb-2">
            {data.recent_events.length === 0 ? (
              <EmptyState title={t('overview.no_events')} className="py-8" />
            ) : (
              <ul className="divide-y divide-border">
                {data.recent_events.slice(0, 6).map((event) => (
                  <EventMiniRow key={event.id} event={event} />
                ))}
              </ul>
            )}
          </CardBody>
        </Card>
      </div>

      {/* networks + anomalies */}
      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>{t('overview.lan_wan')}</CardTitle>
          </CardHeader>
          <CardBody className="p-0 px-4 pb-2">
            {(monitoring.data?.interfaces ?? data.interfaces).length === 0 ? (
              <EmptyState title={t('overview.no_devices')} className="py-8" />
            ) : (
              <div className="divide-y divide-border">
                {(monitoring.data?.interfaces ?? data.interfaces).map((status) => (
                  <InterfaceRow key={status.interface.id} status={status} />
                ))}
              </div>
            )}
          </CardBody>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>{t('overview.connection')}</CardTitle>
          </CardHeader>
          <CardBody className="p-0 px-4 pb-2">
            {data.anomalies.length === 0 ? (
              <EmptyState title={t('overview.no_events')} className="py-8" />
            ) : (
              <ul className="divide-y divide-border">
                {data.anomalies.map((anomaly) => (
                  <li key={anomaly.id} className="py-2.5">
                    <div className="flex items-center justify-between gap-3">
                      <p className="text-sm text-fg">{anomaly.title}</p>
                      <Badge
                        tone={
                          anomaly.severity === 'critical'
                            ? 'danger'
                            : anomaly.severity === 'warning'
                              ? 'warning'
                              : 'neutral'
                        }
                      >
                        {anomaly.severity}
                      </Badge>
                    </div>
                    {anomaly.detail ? <p className="mt-0.5 text-[11px] text-muted">{anomaly.detail}</p> : null}
                  </li>
                ))}
              </ul>
            )}
          </CardBody>
        </Card>
      </div>

      {/* sources summary, useful when debugging discovery */}
      {data.counts.manual > 0 ? (
        <p className="text-[11px] text-muted">
          {data.counts.manual} {t('devices.manual_badge')} · {sourceLabel('manual')}
        </p>
      ) : null}
    </div>
  )
}
