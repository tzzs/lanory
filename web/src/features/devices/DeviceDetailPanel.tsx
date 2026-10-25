import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { X } from 'lucide-react'
import { t } from '@/i18n'
import { useUIStore } from '@/stores/ui'
import {
  useDevices,
  usePatchDevice,
  useDeleteDevice,
  useMergeDevice,
  useCreateDevice as useCreateDeviceLocal,
  useDevice as useDeviceDetail,
} from '@/hooks/useApi'
import { CardHeader, CardTitle, Button, Badge, Input, Select, Modal, FieldRow, StatusDot, EmptyState, Skeleton, Field } from '@/components/ui'
import { AddressGroups } from '@/components/AddressGroups'
import {
  deviceIcon,
  deviceStateLabel,
  deviceTypeLabel,
  formatSpeed,
  formatTimestamp,
  formatRelativeTime,
  formatMac,
  groupDeviceAddresses,
  DEVICE_TYPE_OPTIONS,
} from '@/lib/devices'
import type { Device, DeviceType, EdgeType } from '@/types/api'

/**
 * Add / edit device form.
 *
 * Supports devices without any IP address (Internet, fiber, optical modem,
 * unmanaged switch). Such nodes default to the `unknown` state rather than
 * `offline`, because we simply have no evidence either way.
 */
export function AddDeviceForm({
  formId,
  device,
  onDone,
}: {
  formId?: string
  device?: Device | null
  onDone?: () => void
}) {
  const isEdit = Boolean(device)
  const patch = usePatchDevice()
  const create = useCreateDeviceLocal()

  const [name, setName] = useState(device?.name ?? '')
  const [type, setType] = useState<DeviceType>(device?.type ?? 'unknown')
  const [hostname, setHostname] = useState(device?.hostname ?? '')
  const [ipv4, setIpv4] = useState(device?.primary_ipv4 ?? '')
  const [ipv6, setIpv6] = useState(device?.primary_ipv6 ?? '')
  const [mac, setMac] = useState(device?.primary_mac ?? '')
  const [manufacturer, setManufacturer] = useState(device?.manufacturer ?? '')
  const [model, setModel] = useState(device?.model ?? '')
  const [description, setDescription] = useState(device?.description ?? '')
  const [parentId, setParentId] = useState('')
  const [connectionType, setConnectionType] = useState<EdgeType>('physical_link')
  const [error, setError] = useState<string | null>(null)

  const { data: parentCandidates } = useDevices({ limit: 200 })

  useEffect(() => {
    if (!device) return
    setName(device.name)
    setType(device.type)
    setHostname(device.hostname)
    setIpv4(device.primary_ipv4 ?? '')
    setIpv6(device.primary_ipv6 ?? '')
    setMac(device.primary_mac ?? '')
    setManufacturer(device.manufacturer)
    setModel(device.model)
    setDescription(device.description)
  }, [device])

  async function submit() {
    setError(null)
    if (!name.trim()) {
      setError(t('device.name'))
      return
    }
    try {
      if (isEdit && device) {
        await patch.mutateAsync({
          id: device.id,
          body: { name: name.trim(), type, hostname, manufacturer, model, description },
        })
      } else {
        await create.mutateAsync({
          name: name.trim(),
          type,
          hostname: hostname || undefined,
          ipv4: ipv4 || undefined,
          ipv6: ipv6 || undefined,
          mac: mac || undefined,
          manufacturer: manufacturer || undefined,
          model: model || undefined,
          description: description || undefined,
          parent_device_id: parentId || undefined,
          connection_type: connectionType || undefined,
        })
      }
      onDone?.()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <form
      id={formId}
      className="space-y-3"
      onSubmit={(e) => {
        e.preventDefault()
        void submit()
      }}
    >
      {error ? (
        <p role="alert" className="rounded-md bg-danger/12 px-3 py-2 text-xs text-danger">
          {error}
        </p>
      ) : null}

      <Field label={`${t('device.name')} *`}>
        <Input className="" value={name} onChange={(e) => setName(e.target.value)} required autoFocus />
      </Field>

      <div className="grid gap-4 sm:grid-cols-2">
        <Field label={t('device.type')}>
          <Select className="" value={type} onChange={(e) => setType(e.target.value as DeviceType)}>
            {DEVICE_TYPE_OPTIONS.map((option) => (
              <option key={option} value={option}>
                {deviceTypeLabel(option)}
              </option>
            ))}
          </Select>
        </Field>
        <Field label={t('device.hostname')}>
          <Input className="" value={hostname} onChange={(e) => setHostname(e.target.value)} />
        </Field>
      </div>

      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="IPv4">
          <Input className="font-mono" value={ipv4} onChange={(e) => setIpv4(e.target.value)} placeholder="192.168.1.10" />
        </Field>
        <Field label="IPv6">
          <Input className="font-mono" value={ipv6} onChange={(e) => setIpv6(e.target.value)} placeholder="fd00::1" />
        </Field>
      </div>

      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="MAC">
          <Input className="font-mono" value={mac} onChange={(e) => setMac(e.target.value)} placeholder="AA:BB:CC:DD:EE:FF" />
        </Field>
        <Field label={t('device.manufacturer')}>
          <Input className="" value={manufacturer} onChange={(e) => setManufacturer(e.target.value)} />
        </Field>
      </div>

      <Field label={t('device.model')}>
        <Input className="" value={model} onChange={(e) => setModel(e.target.value)} />
      </Field>

      {!isEdit ? (
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label={t('device.interface')}>
            <Select className="" value={parentId} onChange={(e) => setParentId(e.target.value)}>
              <option value="">{t('app.none')}</option>
              {(parentCandidates?.devices ?? []).map((candidate) => (
                <option key={candidate.id} value={candidate.id}>
                  {candidate.name}
                </option>
              ))}
            </Select>
          </Field>
          <Field label={t('device.edges')}>
            <Select className="" value={connectionType} onChange={(e) => setConnectionType(e.target.value as EdgeType)}>
              <option value="physical_link">{t('edge.physical_link')}</option>
              <option value="wireless_assoc">{t('edge.wireless_assoc')}</option>
              <option value="user_defined">{t('edge.user_defined')}</option>
            </Select>
          </Field>
        </div>
      ) : null}

      <Field label={t('device.description')}>
        <Input className="" value={description} onChange={(e) => setDescription(e.target.value)} />
      </Field>

      {!isEdit ? (
        <p className="text-[11px] text-muted">
          {t('settings.readonly_note')}
        </p>
      ) : null}
    </form>
  )
}

/* -------------------------------------------------------------------------- */
/* Device detail panel                                                        */
/* -------------------------------------------------------------------------- */

/**
 * Shell shared by the loading, error and loaded states of the panel.
 *
 * A 320px side column cannot be afforded on a phone: at 390px it leaves ~70px
 * for the search box, the filters and the table, so browsing breaks down.
 * Below `lg` the panel is therefore an overlay drawer pinned to the right edge,
 * full-bleed up to `max-w-sm`, and it scrolls on its own. From `lg` up it
 * returns to the original in-flow column, which keeps the desktop layout
 * unchanged.
 *
 * Every branch uses this recipe so the panel does not jump size when the data
 * arrives.
 */
const panelShell =
  'fixed inset-y-0 right-0 z-40 w-full max-w-sm shrink-0 overflow-y-auto ' +
  'border-l border-border bg-surface shadow-xl animate-slide-up ' +
  'lg:static lg:z-auto lg:w-80 lg:max-w-none lg:animate-none lg:shadow-none'

export function DeviceDetailPanel({ onClose }: { onClose: () => void }) {
  const selectedDeviceId = useUIStore((s) => s.selectedDeviceId)
  const detail = useDeviceDetail(selectedDeviceId)
  const [editOpen, setEditOpen] = useState(false)
  const [mergeOpen, setMergeOpen] = useState(false)

  if (detail.isLoading) {
    return (
      <aside className={`${panelShell} p-4`}>
        <Skeleton className="h-6 w-32" />
        <Skeleton className="mt-3 h-24" />
        <Skeleton className="mt-3 h-40" />
      </aside>
    )
  }

  if (detail.isError || !detail.data) {
    return (
      <aside className={`${panelShell} p-4`}>
        <EmptyState title={t('app.error')} action={<Button size="sm" onClick={onClose}>{t('app.close')}</Button>} />
      </aside>
    )
  }

  const { device, addresses, identities, interfaces, edges, probes, last_probe, events } = detail.data
  const Icon = deviceIcon(device.type)
  const addressGroups = groupDeviceAddresses(addresses, interfaces)

  return (
    <aside className={`${panelShell} flex flex-col`}>
      <CardHeader className="flex items-start justify-between gap-2">
        <div className="flex min-w-0 items-center gap-2.5">
          <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md bg-surface-2">
            <Icon size={16} className="text-muted" aria-hidden="true" />
          </div>
          <div className="min-w-0">
            <CardTitle className="truncate">{device.name}</CardTitle>
            <p className="truncate text-[11px] text-muted">{deviceTypeLabel(device.type)}</p>
          </div>
        </div>
        <Button variant="ghost" size="icon" onClick={onClose} aria-label={t('app.close')}>
          <X size={15} />
        </Button>
      </CardHeader>

      <div className="min-h-0 flex-1 space-y-5 overflow-y-auto p-4">
        {/* state */}
        <div className="flex items-center gap-2">
          <StatusDot state={device.state} />
          <span className="text-sm font-medium">{deviceStateLabel(device.state)}</span>
          {device.state_reason ? <span className="text-[11px] text-muted">· {device.state_reason}</span> : null}
        </div>

        {device.is_manual ? <Badge tone="accent">{t('devices.manual_badge')}</Badge> : null}
        {device.is_locked ? <Badge>{t('topology.locked')}</Badge> : null}

        {/* identity */}
        <section>
          <h4 className="mb-1.5 text-[11px] font-semibold uppercase tracking-wider text-muted">
            {t('device.identities')}
          </h4>
          <dl className="divide-y divide-border">
            <FieldRow label={t('device.ip')} mono>
              <AddressGroups groups={addressGroups} />
            </FieldRow>
            <FieldRow label={t('device.mac')} mono>
              {device.primary_mac ? formatMac(device.primary_mac) : '—'}
            </FieldRow>
            <FieldRow label={t('device.hostname')}>{device.hostname || '—'}</FieldRow>
            <FieldRow label={t('device.manufacturer')}>{device.manufacturer || '—'}</FieldRow>
            <FieldRow label={t('device.model')}>{device.model || '—'}</FieldRow>
            <FieldRow label={t('device.first_seen')}>{formatTimestamp(device.first_seen)}</FieldRow>
            <FieldRow label={t('device.last_seen')}>{formatRelativeTime(device.last_seen)}</FieldRow>
          </dl>
        </section>

        {/* interfaces + link speed */}
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

        {/* reachability */}
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
            <FieldRow label={t('device.check_source')}>
              {probes[0]?.source_id ? t('source.openwrt') : '—'}
            </FieldRow>
          </dl>
          <p className="mt-1.5 text-[11px] text-muted">{t('monitoring.source_note')}</p>
        </section>

        {/* edges */}
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
                  </span>
                  <span className="shrink-0 text-muted">
                    {edge.user_confirmed ? t('topology.confirmed') : t('topology.unverified')} ·{' '}
                    {Math.round(edge.confidence * 100)}%
                  </span>
                </li>
              ))}
            </ul>
          </section>
        ) : null}

        {/* sources */}
        <section>
          <h4 className="mb-1.5 text-[11px] font-semibold uppercase tracking-wider text-muted">
            {t('device.sources')}
          </h4>
          <div className="flex flex-wrap gap-1.5">
            {(device.discovery_sources ?? []).length === 0 ? (
              <span className="text-xs text-muted">—</span>
            ) : (
              (device.discovery_sources ?? []).map((source) => (
                <Badge key={source}>{t(`source.${source}`)}</Badge>
              ))
            )}
          </div>
        </section>

        {/* history */}
        {events.length > 0 ? (
          <section>
            <h4 className="mb-1.5 text-[11px] font-semibold uppercase tracking-wider text-muted">
              {t('device.history')}
            </h4>
            <ul className="space-y-1.5">
              {events.slice(0, 8).map((event) => (
                <li key={event.id} className="text-xs">
                  <span className="text-muted">{formatRelativeTime(event.timestamp)}</span>{' '}
                  <span className="text-fg">{event.message || t(`event.${event.event_type}`)}</span>
                </li>
              ))}
            </ul>
          </section>
        ) : null}

        {/* raw identities, useful when debugging dedup */}
        {identities.length > 0 ? (
          <section>
            <h4 className="mb-1.5 text-[11px] font-semibold uppercase tracking-wider text-muted">
              {t('device.identities')}
            </h4>
            <ul className="space-y-1">
              {identities.map((identity) => (
                <li key={identity.id} className="flex items-center justify-between gap-2 font-mono text-[11px]">
                  <span className="truncate text-muted">
                    {identity.type}: {identity.value}
                  </span>
                  <span className="shrink-0 text-muted">{Math.round(identity.confidence * 100)}%</span>
                </li>
              ))}
            </ul>
          </section>
        ) : null}
      </div>

      <div className="flex items-center gap-2 border-t border-border p-3">
        <Button size="sm" onClick={() => setEditOpen(true)}>
          {t('app.edit')}
        </Button>
        <Button size="sm" variant="ghost" onClick={() => setMergeOpen(true)}>
          {t('devices.merge')}
        </Button>
        <DeviceActions device={device} />
      </div>

      <Modal
        open={editOpen}
        onClose={() => setEditOpen(false)}
        title={t('app.edit')}
        footer={
          <>
            <Button variant="ghost" onClick={() => setEditOpen(false)}>
              {t('app.cancel')}
            </Button>
            <Button form="edit-device-form" type="submit" variant="primary">
              {t('app.save')}
            </Button>
          </>
        }
      >
        <AddDeviceForm formId="edit-device-form" device={device} onDone={() => setEditOpen(false)} />
      </Modal>

      <MergeDeviceModal open={mergeOpen} onClose={() => setMergeOpen(false)} device={device} />
    </aside>
  )
}

/* -------------------------------------------------------------------------- */
/* Merge                                                                      */
/* -------------------------------------------------------------------------- */

function MergeDeviceModal({
  open,
  onClose,
  device,
}: {
  open: boolean
  onClose: () => void
  device: Device
}) {
  const [sourceId, setSourceId] = useState('')
  const merge = useMergeDevice()
  const { data } = useDevices({ limit: 200 })

  const candidates = (data?.devices ?? []).filter((d) => d.id !== device.id)

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t('devices.merge')}
      description={t('devices.merge_hint')}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            {t('app.cancel')}
          </Button>
          <Button
            variant="primary"
            disabled={!sourceId || merge.isPending}
            onClick={() => {
              merge.mutate(
                { id: device.id, body: { source_device_id: sourceId } },
                { onSuccess: () => onClose() },
              )
            }}
          >
            {t('app.confirm')}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Field label={t('devices.merge_source')}>
          <Select className="" value={sourceId} onChange={(e) => setSourceId(e.target.value)}>
            <option value="">{t('app.none')}</option>
            {candidates.map((candidate) => (
              <option key={candidate.id} value={candidate.id}>
                {candidate.name}
                {candidate.primary_ipv4 ? ` (${candidate.primary_ipv4})` : ''}
              </option>
            ))}
          </Select>
        </Field>
        <p className="text-[11px] text-muted">
          {t('devices.merge_target')}: {device.name}
        </p>
      </div>
    </Modal>
  )
}

/* -------------------------------------------------------------------------- */
/* Delete / hide                                                              */
/* -------------------------------------------------------------------------- */

function DeviceActions({ device }: { device: Device }) {
  const remove = useDeleteDevice()
  const navigate = useNavigate()

  return (
    <Button
      size="sm"
      variant="ghost"
      className="ml-auto text-danger"
      disabled={remove.isPending}
      onClick={() => {
        const mode = device.is_manual ? 'delete' : 'hide'
        const ok = window.confirm(mode === 'delete' ? t('devices.delete_manual_hint') : t('devices.delete_hide_hint'))
        if (!ok) return
        remove.mutate(
          { id: device.id, mode },
          {
            onSuccess: () => {
              void navigate('/devices')
            },
          },
        )
      }}
    >
      {device.is_manual ? t('devices.delete') : t('devices.hide')}
    </Button>
  )
}
