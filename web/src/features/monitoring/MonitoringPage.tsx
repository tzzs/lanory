import { Activity, Play } from 'lucide-react'
import { t } from '@/i18n'
import { useMonitoring, useRunProbe } from '@/hooks/useApi'
import {
  Card,
  CardBody,
  Button,
  Badge,
  Skeleton,
  EmptyState,
  ErrorState,
  Table,
  THead,
  TBody,
  TR,
  TH,
  TD,
  StatusDot,
} from '@/components/ui'
import { formatLatency, formatPercent, formatSpeed, formatBytes, formatRelativeTime } from '@/lib/devices'
import type { InterfaceStatus, ProbeStatus } from '@/types/api'

/* -------------------------------------------------------------------------- */
/* Interface card                                                             */
/* -------------------------------------------------------------------------- */

function InterfaceCard({ status }: { status: InterfaceStatus }) {
  const iface = status.interface
  const up = iface.carrier && iface.oper_state !== 'down' && iface.oper_state !== 'no-carrier'

  return (
    <Card className="p-4">
      <div className="flex items-start justify-between gap-3">
        <div className="flex items-center gap-2.5">
          <StatusDot state={up ? 'online' : 'offline'} />
          <div>
            <p className="font-mono text-sm font-medium text-fg">{iface.name}</p>
            <p className="text-[11px] text-muted">
              {status.role === 'wan' ? 'WAN' : 'LAN'}
              {iface.type === 'bridge' ? ` · ${t('type.switch')}` : ''}
            </p>
          </div>
        </div>
        <Badge tone={up ? 'success' : 'danger'}>{up ? 'UP' : 'NO-CARRIER'}</Badge>
      </div>

      <dl className="mt-3 grid grid-cols-2 gap-x-4 gap-y-1 text-[11px]">
        <div className="flex justify-between">
          <dt className="text-muted">{t('monitoring.port_speed')}</dt>
          <dd className="font-mono text-fg">{formatSpeed(iface.speed_mbps)}</dd>
        </div>
        <div className="flex justify-between">
          <dt className="text-muted">MTU</dt>
          <dd className="font-mono text-fg">{iface.mtu || '—'}</dd>
        </div>
        <div className="flex justify-between">
          <dt className="text-muted">{t('monitoring.rx')}</dt>
          <dd className="font-mono text-fg">{formatBytes(iface.rx_bytes)}</dd>
        </div>
        <div className="flex justify-between">
          <dt className="text-muted">{t('monitoring.tx')}</dt>
          <dd className="font-mono text-fg">{formatBytes(iface.tx_bytes)}</dd>
        </div>
        <div className="flex justify-between">
          <dt className="text-muted">{t('monitoring.errors')}</dt>
          <dd className={cn2(iface.rx_errors + iface.tx_errors > 0, 'font-mono')}>
            {iface.rx_errors + iface.tx_errors}
          </dd>
        </div>
        <div className="flex justify-between">
          <dt className="text-muted">{t('monitoring.dropped')}</dt>
          <dd className={cn2(iface.rx_dropped + iface.tx_dropped > 0, 'font-mono')}>
            {iface.rx_dropped + iface.tx_dropped}
          </dd>
        </div>
      </dl>
    </Card>
  )
}

function cn2(warn: boolean, base: string): string {
  return `${base} ${warn ? 'text-warning' : 'text-fg'}`
}

/* -------------------------------------------------------------------------- */
/* Probe row                                                                  */
/* -------------------------------------------------------------------------- */

function ProbeRow({ status }: { status: ProbeStatus }) {
  const { probe, loss_pct, avg_latency_ms } = status
  const run = useRunProbe()

  return (
    <TR>
      <TD>
        <div className="flex items-center gap-2.5">
          <StatusDot state={probe.state} />
          <div className="min-w-0">
            <p className="truncate font-medium">{probe.name}</p>
            <p className="truncate font-mono text-[11px] text-muted">
              {probe.target}
              {probe.port ? `:${probe.port}` : ''} · {probe.protocol}
            </p>
          </div>
        </div>
      </TD>
      <TD className="text-xs">{t(`state.${probe.state}`)}</TD>
      <TD className="font-mono text-xs">{formatLatency(avg_latency_ms || probe.last_latency_ms)}</TD>
      <TD className="font-mono text-xs">{formatPercent(loss_pct)}</TD>
      <TD className="text-xs text-muted">
        {probe.last_error || (probe.state_reason ? probe.state_reason : '—')}
      </TD>
      <TD className="text-xs text-muted">{probe.last_run_at ? formatRelativeTime(probe.last_run_at) : '—'}</TD>
      <TD>
        <Button
          size="sm"
          variant="ghost"
          disabled={run.isPending}
          onClick={() => run.mutate(probe.id)}
          aria-label={t('monitoring.run_now')}
        >
          <Play size={13} aria-hidden="true" />
        </Button>
      </TD>
    </TR>
  )
}

/* -------------------------------------------------------------------------- */
/* Latency sparkline                                                          */
/* -------------------------------------------------------------------------- */

function Sparkline({ values, label }: { values: number[]; label: string }) {
  if (values.length < 2) return <span className="text-[11px] text-muted">—</span>
  const max = Math.max(...values, 1)
  const width = 96
  const height = 24
  const points = values
    .map((value, index) => {
      const x = (index / (values.length - 1)) * width
      const y = height - (value / max) * height
      return `${x.toFixed(1)},${y.toFixed(1)}`
    })
    .join(' ')

  return (
    <svg width={width} height={height} role="img" aria-label={label} className="overflow-visible">
      <polyline points={points} fill="none" stroke="var(--accent)" strokeWidth={1.5} strokeLinejoin="round" />
    </svg>
  )
}

/* -------------------------------------------------------------------------- */
/* Page                                                                       */
/* -------------------------------------------------------------------------- */

export function MonitoringPage() {
  const { data, isLoading, isError, error, refetch } = useMonitoring()

  if (isLoading) {
    return (
      <div className="space-y-4 p-6">
        <Skeleton className="h-8 w-40" />
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {Array.from({ length: 3 }).map((_, i) => (
            <Skeleton key={i} className="h-32" />
          ))}
        </div>
        <Skeleton className="h-64" />
      </div>
    )
  }

  if (isError || !data) {
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

  return (
    <div className="space-y-5 p-6">
      {/* source caveat - this is a correctness requirement, not decoration */}
      <p className="rounded-md border border-border bg-surface-2 px-3 py-2 text-[11px] text-muted">
        {t('monitoring.source_note')}
      </p>

      {/* interfaces */}
      <section>
        <h2 className="mb-2 text-sm font-semibold text-fg">{t('monitoring.lan_wan')}</h2>
        {data.interfaces.length === 0 ? (
          <Card>
            <EmptyState title={t('overview.no_devices')} />
          </Card>
        ) : (
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {data.interfaces.map((status) => (
              <InterfaceCard key={status.interface.id} status={status} />
            ))}
          </div>
        )}
      </section>

      {/* probes */}
      <section>
        <div className="mb-2 flex items-center justify-between gap-3">
          <h2 className="text-sm font-semibold text-fg">{t('monitoring.device_status')}</h2>
          <Button size="sm" variant="ghost" onClick={() => void refetch()}>
            <Activity size={14} aria-hidden="true" />
            {t('app.refresh')}
          </Button>
        </div>

        <Card>
          {data.probes.length === 0 ? (
            <EmptyState title={t('monitoring.no_probes')} description={t('monitoring.add_probe')} />
          ) : (
            <Table>
              <THead>
                <TR className="hover:bg-transparent">
                  <TH>{t('monitoring.probe_name')}</TH>
                  <TH>{t('devices.col_status')}</TH>
                  <TH>{t('monitoring.latency')}</TH>
                  <TH>{t('monitoring.packet_loss')}</TH>
                  <TH>{t('events.col_message')}</TH>
                  <TH>{t('devices.col_last_seen')}</TH>
                  <TH />
                </TR>
              </THead>
              <TBody>
                {data.probes.map((status) => (
                  <ProbeRow key={status.probe.id} status={status} />
                ))}
              </TBody>
            </Table>
          )}
        </Card>
      </section>

      {/* latency trend for the first few probes */}
      {data.probes.some((p) => (p.recent?.length ?? 0) >= 2) ? (
        <section>
          <h2 className="mb-2 text-sm font-semibold text-fg">{t('monitoring.latency')}</h2>
          <Card>
            <CardBody className="space-y-3">
              {data.probes
                .filter((p) => (p.recent?.length ?? 0) >= 2)
                .slice(0, 6)
                .map((status) => (
                  <div key={status.probe.id} className="flex items-center justify-between gap-4">
                    <div className="min-w-0">
                      <p className="truncate text-sm text-fg">{status.probe.name}</p>
                      <p className="text-[11px] text-muted">
                        {t('monitoring.avg_latency')} {formatLatency(status.avg_latency_ms)} ·{' '}
                        {t('monitoring.packet_loss')} {formatPercent(status.loss_pct)}
                      </p>
                    </div>
                    <Sparkline
                      values={[...(status.recent ?? [])].reverse().map((r) => r.latency_ms)}
                      label={`${status.probe.name} ${t('monitoring.latency')}`}
                    />
                  </div>
                ))}
            </CardBody>
          </Card>
        </section>
      ) : null}
    </div>
  )
}
