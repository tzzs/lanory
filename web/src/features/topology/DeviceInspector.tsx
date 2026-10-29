import { useMemo, useState, type ReactNode } from 'react'
import { useNavigate } from 'react-router-dom'
import { ArrowDown, ArrowRight, ArrowUp, Lock, X } from 'lucide-react'
import { t } from '@/i18n'
import { cn } from '@/lib/cn'
import { useUIStore } from '@/stores/ui'
import { useDevice, useTopology } from '@/hooks/useApi'
import { Button, Badge, StatusDot, Skeleton, EmptyState } from '@/components/ui'
import {
  deviceIcon,
  deviceStateLabel,
  deviceTypeLabel,
  formatMac,
  formatRelativeTime,
  formatSpeed,
  groupDeviceAddresses,
  type AddressGroup,
} from '@/lib/devices'
import type { NetworkInterface, TopologyEdge } from '@/types/api'

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
 * Device inspector used by the topology page.
 *
 * The panel answers three questions in this order, which is also the order
 * they are rendered in: *who is this*, *where is it*, *is it healthy*. Anything
 * that does not serve one of those is left to the full device page — the
 * topology popup is a glance, not a record.
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

  return (
    <DeviceInspectorBody
      // Remounting on selection change resets the address expansion, so a
      // device that was expanded does not hand its state to the next one.
      key={selectedDeviceId}
      deviceId={selectedDeviceId}
      onClose={() => selectDevice(null)}
    />
  )
}

function DeviceInspectorBody({ deviceId, onClose }: { deviceId: string; onClose: () => void }) {
  const navigate = useNavigate()
  const [showAllAddresses, setShowAllAddresses] = useState(false)
  const { data, isLoading, isError } = useDevice(deviceId)

  // The topology payload is already in the query cache: the inspector is only
  // reachable from the topology page. Reading it here is what lets a link row
  // name the device at the other end instead of printing the edge type.
  const { data: topology } = useTopology()
  const deviceNames = useMemo(() => {
    const names = new Map<string, string>()
    for (const node of topology?.nodes ?? []) {
      if (node.device) names.set(node.device.id, node.device.name)
    }
    return names
  }, [topology])

  const addressGroups = useMemo(
    () => groupDeviceAddresses(data?.addresses, data?.interfaces),
    [data?.addresses, data?.interfaces],
  )
  const addressRows = useMemo(() => buildAddressRows(addressGroups), [addressGroups])

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

  const { device, interfaces, edges, last_probe } = data
  const Icon = deviceIcon(device.type)
  const sources = device.discovery_sources ?? []
  const collapsedAddressRows = primaryAddressRows(addressRows)

  // Only the facts that were actually observed. A row of "厂商 —" tells the
  // user nothing except that the collector found nothing, which the absence of
  // the row says just as clearly and without spending a line.
  const facts: { label: string; value: ReactNode; mono?: boolean }[] = []
  if (device.primary_mac) {
    facts.push({ label: t('device.mac'), value: formatMac(device.primary_mac), mono: true })
  }
  if (device.manufacturer) facts.push({ label: t('device.manufacturer'), value: device.manufacturer })
  if (device.model) facts.push({ label: t('device.model'), value: device.model })
  if (device.hostname && device.hostname !== device.name) {
    facts.push({ label: t('device.hostname'), value: device.hostname, mono: true })
  }

  return (
    <aside className={`${panelShell} flex flex-col`}>
      <header className="flex items-start gap-2.5 border-b border-border p-4">
        <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-surface-2">
          <Icon size={18} className="text-muted" aria-hidden="true" />
        </div>
        <div className="min-w-0 flex-1">
          <h3 title={device.name} className="truncate text-sm font-semibold text-fg">
            {device.name}
          </h3>
          <div className="mt-0.5 flex items-center gap-1.5 text-[11px] text-muted">
            <StatusDot state={device.state} />
            <span className="font-medium text-fg">{deviceStateLabel(device.state)}</span>
            <span aria-hidden="true">·</span>
            <span className="truncate">{deviceTypeLabel(device.type)}</span>
            {device.is_locked ? (
              <Lock size={11} className="shrink-0" aria-label={t('topology.locked')} />
            ) : null}
          </div>
        </div>
        <Button
          variant="ghost"
          size="icon"
          onClick={onClose}
          aria-label={t('app.close')}
          className="-mr-1.5 -mt-1.5 shrink-0"
        >
          <X size={15} />
        </Button>
      </header>

      <div className="min-h-0 flex-1 space-y-5 overflow-y-auto p-4">
        {/* Why the device is in this state, when the engine knows. Truncated to
            a single line: the full sentence is on the device page. */}
        {device.state_reason ? (
          <p className="-mt-1 truncate text-[11px] text-muted" title={device.state_reason}>
            {device.state_reason}
          </p>
        ) : null}

        {/* Identity: one line per address, one line per observed fact. */}
        <div className="space-y-2">
          {addressRows.length > 0 ? (
            <div className="overflow-hidden rounded-md border border-border">
              <ul className="divide-y divide-border">
                {(showAllAddresses ? addressRows : collapsedAddressRows).map((row) => (
                  <li
                    key={row.key}
                    className="flex items-baseline justify-between gap-3 px-2.5 py-1.5"
                    title={row.title}
                  >
                    <span className="min-w-0 truncate font-mono text-xs text-fg">{row.address}</span>
                    {row.label ? (
                      <span className="shrink-0 text-[10px] uppercase tracking-wide text-muted">{row.label}</span>
                    ) : null}
                  </li>
                ))}
              </ul>
              {addressRows.length > collapsedAddressRows.length ? (
                <button
                  type="button"
                  onClick={() => setShowAllAddresses((open) => !open)}
                  className="w-full border-t border-border px-2.5 py-1 text-left text-[10px] text-muted transition-colors hover:text-fg"
                >
                  {showAllAddresses
                    ? t('device.show_fewer_addresses')
                    : t('device.show_all_addresses', { count: addressRows.length })}
                </button>
              ) : null}
            </div>
          ) : (
            <p className="text-xs text-muted">{t('device.no_address')}</p>
          )}

          {facts.length > 0 ? (
            <dl className="divide-y divide-border">
              {facts.map((fact) => (
                <div key={fact.label} className="flex items-baseline justify-between gap-3 py-1.5">
                  <dt className="shrink-0 text-xs text-muted">{fact.label}</dt>
                  <dd className={cn('min-w-0 truncate text-right text-xs text-fg', fact.mono && 'font-mono')}>
                    {fact.value}
                  </dd>
                </div>
              ))}
            </dl>
          ) : null}
        </div>

        {device.is_manual ? <Badge tone="accent">{t('devices.manual_badge')}</Badge> : null}

        {interfaces.length > 0 ? (
          <Section title={t('device.interface')}>
            <div className="flex flex-wrap gap-1.5">
              {interfaces.map((iface) => (
                <InterfaceChip key={iface.id} iface={iface} />
              ))}
            </div>
          </Section>
        ) : null}

        {edges.length > 0 ? (
          <Section title={t('device.edges')}>
            <ul className="space-y-1">
              {edges.map((edge) => (
                <LinkRow key={edge.id} edge={edge} deviceId={device.id} names={deviceNames} />
              ))}
            </ul>
          </Section>
        ) : null}

        <Section title={t('device.reachability')}>
          <dl className="divide-y divide-border">
            <FactRow label={t('device.last_check')}>
              {last_probe ? (
                <span className={last_probe.success ? 'text-success' : 'text-danger'}>
                  {last_probe.success ? 'OK' : last_probe.status}
                  {last_probe.latency_ms > 0 ? ` · ${last_probe.latency_ms} ms` : ''}
                </span>
              ) : (
                '—'
              )}
            </FactRow>
            <FactRow label={t('device.last_seen')}>{formatRelativeTime(device.last_seen)}</FactRow>
            {sources.length > 0 ? (
              <div className="flex items-start justify-between gap-3 py-1.5">
                <dt className="shrink-0 text-xs text-muted">{t('device.sources')}</dt>
                <dd className="flex flex-wrap justify-end gap-1">
                  {sources.map((source) => (
                    <Badge key={source}>{t(`source.${source}`)}</Badge>
                  ))}
                </dd>
              </div>
            ) : null}
          </dl>
        </Section>
      </div>

      {/* The popup is a glance; editing, merging and history live on the
          device page, and this is the only way to get there from the map. */}
      <div className="border-t border-border p-3">
        <Button
          size="sm"
          variant="secondary"
          className="w-full"
          onClick={() => navigate(`/devices/${device.id}`)}
        >
          {t('device.open_details')}
          <ArrowRight size={13} aria-hidden="true" />
        </Button>
      </div>
    </aside>
  )
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section>
      <h4 className="mb-1.5 text-[11px] font-semibold uppercase tracking-wider text-muted">{title}</h4>
      {children}
    </section>
  )
}

function FactRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-3 py-1.5">
      <dt className="shrink-0 text-xs text-muted">{label}</dt>
      <dd className="min-w-0 text-right text-xs text-fg">{children}</dd>
    </div>
  )
}

interface AddressRow {
  key: string
  address: string
  /** The side, plus the subnet when it is short enough to sit on one line. */
  label: string
  /** The full description, including the IPv6 subnet the label leaves out. */
  title: string
  side: string
}

/**
 * Flattens the address groups into one row per address.
 *
 * The subnet is only shown for IPv4. An IPv6 prefix ("LAN · 2001:DB8:100::/64")
 * is wider than the label column and would push the address out of the panel,
 * so it moves to the row's tooltip instead — still reachable, no longer
 * breaking the layout.
 */
function buildAddressRows(groups: AddressGroup[]): AddressRow[] {
  return groups.flatMap((group) => {
    const side = group.side && group.side !== 'unknown' ? t(`role.${group.side}`) : ''
    return group.addresses.map((a) => ({
      key: `${group.cidr}|${a.address}`,
      address: a.address,
      label: [side, a.family === 'ipv4' ? group.cidr : ''].filter(Boolean).join(' · '),
      title: [side, group.cidr].filter(Boolean).join(' · '),
      side: group.side ?? 'unknown',
    }))
  })
}

/**
 * The rows shown while the address block is collapsed: one per side.
 *
 * A router carries an IPv4 and an IPv6 address on each side, and printing all
 * of them buries the two that identify the box. The side is what distinguishes
 * them, so one address per side is the shortest list that still says "this is
 * the LAN address and this is the WAN address".
 */
function primaryAddressRows(rows: AddressRow[]): AddressRow[] {
  const seen = new Set<string>()
  return rows.filter((row) => {
    if (seen.has(row.side)) return false
    seen.add(row.side)
    return true
  })
}

/**
 * A compact interface chip.
 *
 * The speed is only appended when it is known: every interface on an OpenWrt
 * box currently reports 0, and a column of em dashes reads as broken data
 * rather than as missing data.
 */
function InterfaceChip({ iface }: { iface: NetworkInterface }) {
  const speed = formatSpeed(iface.speed_mbps)
  const role = iface.role && iface.role !== 'unknown' ? t(`role.${iface.role}`) : ''
  return (
    <span
      className="inline-flex items-center gap-1.5 rounded-md border border-border bg-surface-2/60 px-1.5 py-1 font-mono text-[10px] text-fg"
      title={`${iface.name}${role ? ` · ${role}` : ''}${speed !== '—' ? ` · ${speed}` : ''}`}
    >
      <span
        className={cn('h-1.5 w-1.5 shrink-0 rounded-full', iface.carrier ? 'bg-success' : 'bg-muted')}
        aria-hidden="true"
      />
      <span>{iface.name}</span>
      {role ? <span className="text-muted">{role}</span> : null}
      {speed !== '—' ? <span className="text-muted">{speed}</span> : null}
    </span>
  )
}

/**
 * One link of the device, named after the device at the other end.
 *
 * The stored edge points from parent to child, so the direction tells us
 * whether the peer is upstream (this device hangs off it) or downstream (it
 * hangs off this device). That is the question a topology popup exists to
 * answer; the edge type alone ("端口学习") answers nothing.
 */
function LinkRow({
  edge,
  deviceId,
  names,
}: {
  edge: TopologyEdge
  deviceId: string
  names: Map<string, string>
}) {
  const upstream = edge.target_id === deviceId
  const peerId = upstream ? edge.source_id : edge.target_id
  const peer = names.get(peerId) ?? t('app.unknown')
  const Arrow = upstream ? ArrowUp : ArrowDown
  const direction = t(upstream ? 'topology.upstream' : 'topology.downstream')

  return (
    <li
      className="flex items-center gap-2 text-xs"
      title={`${direction} · ${t(`edge.${edge.edge_type}`)}`}
    >
      <Arrow size={12} className="shrink-0 text-muted" aria-label={direction} />
      <span className="min-w-0 flex-1 truncate text-fg">{peer}</span>
      {edge.label ? (
        <span className="shrink-0 font-mono text-[10px] text-muted">{edge.label}</span>
      ) : null}
      {edge.unverified ? (
        <span className="shrink-0 text-[10px] text-warning">{t('topology.unverified')}</span>
      ) : null}
    </li>
  )
}
