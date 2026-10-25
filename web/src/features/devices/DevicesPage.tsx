import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Plus, Search, X } from 'lucide-react'
import { t } from '@/i18n'
import { useDevices, useDevice, useCreateDevice } from '@/hooks/useApi'
import { useUIStore } from '@/stores/ui'
import {
  Card,
  Button,
  Input,
  Select,
  Checkbox,
  Table,
  THead,
  TBody,
  TR,
  TH,
  TD,
  Badge,
  Skeleton,
  EmptyState,
  ErrorState,
  StatusDot,
  Modal,
  PageHeader,
} from '@/components/ui'
import {
  deviceIcon,
  deviceStateLabel,
  deviceTypeLabel,
  formatRelativeTime,
  DEVICE_TYPE_OPTIONS,
  DEVICE_STATE_OPTIONS,
} from '@/lib/devices'
import { DeviceDetailPanel } from '@/features/devices/DeviceDetailPanel'
import { AddDeviceForm } from '@/features/devices/AddDeviceForm'
import type { Device } from '@/types/api'

/* -------------------------------------------------------------------------- */
/* Filters                                                                    */
/* -------------------------------------------------------------------------- */

function DeviceFilters() {
  const filters = useUIStore((s) => s.deviceFilters)
  const setFilter = useUIStore((s) => s.setDeviceFilter)
  const reset = useUIStore((s) => s.resetDeviceFilters)

  const dirty =
    filters.query !== '' || filters.type !== '' || filters.state !== '' || filters.source !== '' || filters.iface !== '' || filters.newOnly

  return (
    <Card className="p-3">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-[220px] flex-1">
          <Search
            size={14}
            className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-muted"
            aria-hidden="true"
          />
          <Input
            className="pl-8"
            placeholder={t('devices.search_placeholder')}
            value={filters.query}
            onChange={(e) => setFilter('query', e.target.value)}
            aria-label={t('app.search')}
          />
        </div>

        <Select
          className="w-auto min-w-[130px]"
          value={filters.type}
          onChange={(e) => setFilter('type', e.target.value as typeof filters.type)}
          aria-label={t('devices.filter_type')}
        >
          <option value="">{t('devices.filter_type')}</option>
          {DEVICE_TYPE_OPTIONS.map((type) => (
            <option key={type} value={type}>
              {deviceTypeLabel(type)}
            </option>
          ))}
        </Select>

        <Select
          className="w-auto min-w-[120px]"
          value={filters.state}
          onChange={(e) => setFilter('state', e.target.value as typeof filters.state)}
          aria-label={t('devices.filter_state')}
        >
          <option value="">{t('devices.filter_state')}</option>
          {DEVICE_STATE_OPTIONS.map((state) => (
            <option key={state} value={state}>
              {deviceStateLabel(state)}
            </option>
          ))}
        </Select>

        <Select
          className="w-auto min-w-[130px]"
          value={filters.iface}
          onChange={(e) => setFilter('iface', e.target.value)}
          aria-label={t('devices.filter_interface')}
        >
          <option value="">{t('devices.filter_interface')}</option>
          {['eth0', 'eth1', 'eth2', 'eth3', 'br-lan'].map((name) => (
            <option key={name} value={name}>
              {name}
            </option>
          ))}
        </Select>

        <Checkbox
          label={t('devices.filter_new')}
          checked={filters.newOnly}
          onChange={(e) => setFilter('newOnly', e.target.checked)}
        />

        {dirty ? (
          <Button variant="ghost" size="sm" onClick={reset}>
            <X size={13} aria-hidden="true" />
            {t('app.cancel')}
          </Button>
        ) : null}
      </div>
    </Card>
  )
}

/* -------------------------------------------------------------------------- */
/* Table                                                                      */
/* -------------------------------------------------------------------------- */

function DeviceRow({ device, onSelect }: { device: Device; onSelect: (id: string) => void }) {
  const Icon = deviceIcon(device.type)
  return (
    <TR
      className="cursor-pointer focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/60"
      onClick={() => onSelect(device.id)}
      tabIndex={0}
      role="button"
      aria-label={device.name}
      onKeyDown={(e) => {
        if (e.key !== 'Enter' && e.key !== ' ') return
        // Space would otherwise scroll the page instead of activating the row.
        if (e.key === ' ') e.preventDefault()
        onSelect(device.id)
      }}
    >
      <TD>
        <div className="flex items-center gap-2.5">
          <StatusDot state={device.state} />
          <Icon size={15} strokeWidth={1.75} className="shrink-0 text-muted" aria-hidden="true" />
          <span className="truncate font-medium">{device.name}</span>
          {device.is_manual ? <Badge tone="accent">{t('devices.manual_badge')}</Badge> : null}
          {device.is_locked ? <Badge>{t('topology.locked')}</Badge> : null}
        </div>
      </TD>
      <TD className="text-muted">{deviceTypeLabel(device.type)}</TD>
      <TD className="font-mono text-xs">{device.primary_ipv4 || '—'}</TD>
      <TD className="font-mono text-xs">{device.primary_mac ? device.primary_mac.toUpperCase() : '—'}</TD>
      <TD className="text-muted">{(device.discovery_sources ?? []).map((s) => t(`source.${s}`)).join(', ') || '—'}</TD>
      <TD>
        <span className="text-xs">{deviceStateLabel(device.state)}</span>
      </TD>
      <TD className="text-xs text-muted">{formatRelativeTime(device.last_seen)}</TD>
    </TR>
  )
}

/* -------------------------------------------------------------------------- */
/* Page                                                                       */
/* -------------------------------------------------------------------------- */

export function DevicesPage() {
  const { id: routeId } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const filters = useUIStore((s) => s.deviceFilters)
  const selectDevice = useUIStore((s) => s.selectDevice)
  const setSelectedDevice = useUIStore((s) => s.setSelectedDevice)
  const detailPanelOpen = useUIStore((s) => s.detailPanelOpen)
  const setDetailPanelOpen = useUIStore((s) => s.setDetailPanelOpen)

  const [addOpen, setAddOpen] = useState(false)
  const [sort, setSort] = useState('last_seen')
  const [order, setOrder] = useState<'asc' | 'desc'>('desc')

  const query = useMemo(
    () => ({
      query: filters.query || undefined,
      type: filters.type || undefined,
      state: filters.state || undefined,
      source: filters.source || undefined,
      interface: filters.iface || undefined,
      sort,
      order,
      online_first: true,
      limit: 200,
    }),
    [filters, sort, order],
  )

  const { data, isLoading, isFetching, isError, error, refetch } = useDevices(query)
  const detail = useDevice(routeId ?? null)

  // Keep the store in sync when a device is opened by URL.
  useEffect(() => {
    if (detail.data?.device) setSelectedDevice(detail.data.device)
  }, [detail.data, setSelectedDevice])

  // Reflect the "new only" filter as a client-side cut-off (24h) until the
  // backend exposes a dedicated parameter.
  const devices = useMemo(() => {
    if (!data?.devices) return []
    if (!filters.newOnly) return data.devices
    const cutoff = Date.now() - 24 * 3600 * 1000
    return data.devices.filter((d) => new Date(d.first_seen).getTime() >= cutoff)
  }, [data, filters.newOnly])

  return (
    <div className="flex h-full min-h-0">
      <div className="flex min-w-0 flex-1 flex-col gap-4 p-6">
        <PageHeader
          title={t('devices.title')}
          description={t('devices.count', { count: data?.total ?? 0 })}
          actions={
            <>
              <Select
                className="w-auto min-w-[120px]"
                value={sort}
                onChange={(e) => setSort(e.target.value)}
                aria-label={t('devices.sort_name')}
              >
                <option value="last_seen">{t('devices.sort_last_seen')}</option>
                <option value="name">{t('devices.sort_name')}</option>
                <option value="ip">{t('devices.sort_ip')}</option>
                <option value="type">{t('devices.sort_type')}</option>
              </Select>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => setOrder(order === 'asc' ? 'desc' : 'asc')}
                aria-label="toggle order"
              >
                {order === 'asc' ? '↑' : '↓'}
              </Button>
              <Button variant="primary" size="sm" onClick={() => setAddOpen(true)}>
                <Plus size={14} aria-hidden="true" />
                {t('devices.add')}
              </Button>
            </>
          }
        />

        <DeviceFilters />

        <Card className="min-h-0 flex-1 overflow-auto" aria-busy={isFetching || undefined}>
          {/* The table's DOM has to outlive its data. The list is re-fetched on
              every poll and on every filter keystroke, and each of those used
              to swap the rows for a skeleton (or for the error state when a
              poll failed), which unmounts the row the keyboard user is standing
              on and drops focus to <body>. So: the skeleton is only for the
              first load, and a failed or emptied background refetch is reported
              *beside* the rows that are still on screen rather than instead of
              them. `aria-busy` tells a screen reader the list is refreshing
              without moving anything. */}
          {isLoading ? (
            <div className="space-y-2 p-4">
              {Array.from({ length: 8 }).map((_, i) => (
                <Skeleton key={i} className="h-10" />
              ))}
            </div>
          ) : devices.length === 0 ? (
            // No rows to preserve: an empty result is an empty state, unless the
            // request itself failed, in which case the error owns the card.
            isError ? (
              <ErrorState
                title={t('app.error')}
                description={error instanceof Error ? error.message : undefined}
                action={
                  <Button variant="primary" onClick={() => void refetch()}>
                    {t('app.retry')}
                  </Button>
                }
              />
            ) : (
              <EmptyState title={t('devices.empty')} description={t('devices.subtitle')} />
            )
          ) : (
            <>
              {isError ? (
                <div role="alert" className="flex items-center justify-between gap-3 border-b border-danger/30 bg-danger/12 px-3 py-2 text-xs text-danger">
                  <span>{error instanceof Error ? error.message : t('app.error')}</span>
                  <Button variant="ghost" size="sm" onClick={() => void refetch()}>
                    {t('app.retry')}
                  </Button>
                </div>
              ) : null}
              <Table>
                <THead>
                  <TR className="hover:bg-transparent">
                    <TH>{t('devices.col_name')}</TH>
                    <TH>{t('devices.col_type')}</TH>
                    <TH>{t('devices.col_ip')}</TH>
                    <TH>{t('devices.col_mac')}</TH>
                    <TH>{t('devices.col_source')}</TH>
                    <TH>{t('devices.col_status')}</TH>
                    <TH>{t('devices.col_last_seen')}</TH>
                  </TR>
                </THead>
                <TBody>
                  {devices.map((device) => (
                    <DeviceRow
                      key={device.id}
                      device={device}
                      onSelect={(id) => {
                        selectDevice(id)
                        void navigate(`/devices/${id}`)
                      }}
                    />
                  ))}
                </TBody>
              </Table>
            </>
          )}
        </Card>
      </div>

      {detailPanelOpen && routeId ? <DeviceDetailPanel onClose={() => { setDetailPanelOpen(false); void navigate('/devices') }} /> : null}

      <AddDeviceModal open={addOpen} onClose={() => setAddOpen(false)} />
    </div>
  )
}

function AddDeviceModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const create = useCreateDevice()
  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t('devices.add')}
      description={t('topology.add_device')}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            {t('app.cancel')}
          </Button>
          <Button form="add-device-form" type="submit" variant="primary" disabled={create.isPending}>
            {create.isPending ? t('app.loading') : t('app.save')}
          </Button>
        </>
      }
    >
      <AddDeviceForm
        formId="add-device-form"
        onDone={() => {
          onClose()
        }}
      />
    </Modal>
  )
}
