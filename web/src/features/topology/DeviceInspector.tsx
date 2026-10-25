import { t } from '@/i18n'
import { useUIStore } from '@/stores/ui'
import { useDevice } from '@/hooks/useApi'
import { Button, CardHeader, CardTitle, Badge, FieldRow, StatusDot, Skeleton, EmptyState } from '@/components/ui'
import { deviceIcon, formatTimestamp, formatRelativeTime, formatMac, formatSpeed } from '@/lib/devices'

/**
 * Shell shared by every state of the inspector.
 *
 * A 320px side column cannot be afforded on a phone: at 390px it leaves ~70px
 * for the canvas and its toolbar. Below `lg` the inspector is therefore an
 * overlay drawer pinned to the right edge, full-bleed up to `max-w-sm`, and it
 * scrolls on its own. From `lg` up it returns to the original in-flow column,
 * so the desktop layout is unchanged.
 *
 * Every branch uses this recipe so the panel does not jump size when the data
 * arrives.
 */
const panelShell =
  'fixed inset-y-0 right-0 z-40 w-full max-w-sm shrink-0 overflow-y-auto ' +
  'border-l border-border bg-surface shadow-xl animate-slide-up ' +
  'lg:static lg:z-auto lg:w-80 lg:max-w-none lg:animate-none lg:shadow-none'

/**
 * Device inspector used by the topology page. Reuses the shared device detail
 * layout so a device looks the same wherever it is opened.
 */
export function DeviceInspector() {
  const selectedDeviceId = useUIStore((s) => s.selectedDeviceId)
  const selectDevice = useUIStore(s => s.selectDevice)

  if (!selectedDeviceId) {
    return (
      <aside className="hidden w-80 shrink-0 border-l border-border bg-surface lg:block">
        <div className="p-4">
          <p className="text-xs text-muted">{t('topology.select_node')}</p>
        </div>
      </aside>
    )
  }

  return <DeviceInspectorBody deviceId={selectedDeviceId} onClose={() => selectDevice(null)} />
}

function DeviceInspectorBody({ deviceId, onClose }: { deviceId: string; onClose: () => void }) {
  const { data, isLoading, isError } = useDevice(deviceId)

  if (isLoading) {
    return (
      <aside className={`${panelShell} p-4`}>
        <Skeleton className="h-6 w-32" />
        <Skeleton className="mt-3 h-40" />
      </aside>
    )
  }

  if (isError || !data) {
    return (
      <aside className={panelShell}>
        <EmptyState title={t('app.error')} action={<Button size="sm" onClick={onClose}>{t('app.close')}</Button>} />
      </aside>
    )
  }

  const { device, addresses, interfaces, edges, last_probe } = data
  const Icon = deviceIcon(device.type)

  return (
    <aside className={`${panelShell} flex flex-col`}>
      <CardHeader>
        <div className="flex min-w-0 items-center gap-2.5">
          <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md bg-surface-2">
            <Icon size={16} className="text-muted" aria-hidden="true" />
          </div>
          <div className="min-w-0">
            <CardTitle className="truncate">{device.name}</CardTitle>
            <p className="truncate text-[11px] text-muted">{t(`type.${device.type}`)}</p>
          </div>
        </div>
        <Button variant="ghost" size="sm" onClick={onClose}>
          {t('app.close')}
        </Button>
      </CardHeader>

      <div className="min-h-0 flex-1 space-y-5 overflow-y-auto p-4">
        <div className="flex items-center gap-2">
          <StatusDot state={device.state} />
          <span className="text-sm font-medium">{t(`state.${device.state}`)}</span>
          {device.state_reason ? <span className="text-[11px] text-muted">· {device.state_reason}</span> : null}
        </div>

        <dl className="divide-y divide-border">
          <FieldRow label={t('device.ip')} mono>
            {addresses.map((a) => a.address).join(', ') || '—'}
          </FieldRow>
          <FieldRow label={t('device.mac')} mono>
            {device.primary_mac ? formatMac(device.primary_mac) : '—'}
          </FieldRow>
          <FieldRow label={t('device.manufacturer')}>{device.manufacturer || '—'}</FieldRow>
          <FieldRow label={t('device.model')}>{device.model || '—'}</FieldRow>
        </dl>

        {interfaces.length > 0 ? (
          <section>
            <h4 className="mb-1.5 text-[11px] font-semibold uppercase tracking-wider text-muted">
              {t('device.interface')}
            </h4>
            <dl className="divide-y divide-border">
              {interfaces.map((iface) => (
                <FieldRow key={iface.id} label={iface.name} mono>
                  {iface.carrier ? formatSpeed(iface.speed_mbps) : t('monitoring.port_state') + ': down'}
                </FieldRow>
              ))}
            </dl>
          </section>
        ) : null}

        <section>
          <h4 className="mb-1.5 text-[11px] font-semibold uppercase tracking-wider text-muted">
            {t('device.reachability')}
          </h4>
          <dl className="divide-y divide-border">
            <FieldRow label={t('device.last_check')}>
              {last_probe ? (
                <span className={last_probe.success ? 'text-success' : 'text-danger'}>
                  {last_probe.success ? 'OK' : last_probe.status}
                  {last_probe.latency_ms > 0 ? ` · ${last_probe.latency_ms} ms` : ''}
                </span>
              ) : (
                '—'
              )}
            </FieldRow>
            <FieldRow label={t('device.last_seen')}>{formatRelativeTime(device.last_seen)}</FieldRow>
            <FieldRow label={t('device.first_seen')}>{formatTimestamp(device.first_seen)}</FieldRow>
          </dl>
          <p className="mt-1.5 text-[11px] text-muted">{t('monitoring.source_note')}</p>
        </section>

        {edges.length > 0 ? (
          <section>
            <h4 className="mb-1.5 text-[11px] font-semibold uppercase tracking-wider text-muted">
              {t('device.edges')}
            </h4>
            <ul className="space-y-1.5">
              {edges.map((edge) => (
                <li key={edge.id} className="flex items-center justify-between gap-2 text-xs">
                  <span className="truncate text-fg">
                    {edge.source_id === device.id ? '→ ' : '← '}
                    {t(`edge.${edge.edge_type}`)}
                    {edge.label ? ` · ${edge.label}` : ''}
                  </span>
                  <span className="shrink-0 text-muted">
                    {edge.user_confirmed ? t('topology.confirmed') : t('topology.unverified')}
                  </span>
                </li>
              ))}
            </ul>
          </section>
        ) : null}

        <div className="flex flex-wrap gap-1.5">
          {device.is_manual ? <Badge tone="accent">{t('devices.manual_badge')}</Badge> : null}
          {device.is_locked ? <Badge>{t('topology.locked')}</Badge> : null}
          {(device.discovery_sources ?? []).map((source) => (
            <Badge key={source}>{t(`source.${source}`)}</Badge>
          ))}
        </div>
      </div>
    </aside>
  )
}
