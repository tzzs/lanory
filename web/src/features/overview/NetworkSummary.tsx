import { Link } from 'react-router-dom'
import { ArrowUpRight, Router, EthernetPort, Cable } from 'lucide-react'
import { useMonitoring, useTopology } from '@/hooks/useApi'
import { t } from '@/i18n'
import { formatSpeed } from '@/lib/devices'
import { Badge } from '@/components/ui'
import type { Overview } from '@/types/api'

/** Compact view of the same observed port relationships used by the full map. */
export function NetworkSummary({ overview }: { overview: Overview }) {
  const topology = useTopology()
  const monitoring = useMonitoring()
  const interfaces = monitoring.data?.interfaces ?? overview.interfaces
  const self = topology.data?.nodes.find(n => n.kind === 'device' && n.device?.is_self)
  const lanNetwork = topology.data?.nodes.find(n => n.kind === 'network' && !n.unresolved && n.network?.role === 'lan')?.network
  const routerAddress = self?.primary_ip || lanNetwork?.gateway_ip || lanNetwork?.cidr || '—'
  const wan = interfaces.find(s => s.interface.role === 'wan' && s.interface.carrier)
    ?? interfaces.find(s => s.interface.role === 'wan')
  const ports = interfaces.filter(s => s.interface.role === 'lan' && s.interface.is_physical)
  const edges = topology.data?.edges ?? []
  const mapped = new Set(edges.filter(e => e.edge_type === 'observed_mac_port').map(e => e.target_id))
  const unplaced = topology.data?.nodes.filter(n => n.kind === 'device' && !n.device?.is_self && !mapped.has(n.id)).length

  return (
    <div className="px-5 py-5">
      <div className="flex flex-col items-center border-b border-border pb-5">
        <div className="flex flex-wrap items-center justify-center gap-2 rounded-md border border-border bg-surface-2 px-4 py-3 text-xs">
          <Cable size={16} className="text-accent" />
          <span className="font-mono font-semibold">WAN · {wan?.interface.name ?? '—'}</span>
          <Badge tone={wan?.interface.carrier ? 'success' : 'neutral'}>{wan?.interface.carrier ? 'UP' : 'DOWN'}</Badge>
          <span className="font-mono text-muted">{formatSpeed(wan?.interface.speed_mbps)}</span>
        </div>
        <div className="h-6 border-l border-border" aria-hidden="true" />
        <div className="flex items-center gap-3">
          <div className="rounded-lg bg-accent/10 p-3"><Router size={22} className="text-accent" /></div>
          <div>
            <p className="text-sm font-semibold">{self?.device?.name ?? t('type.router')}</p>
            <p className="mt-1 font-mono text-xs text-muted">{routerAddress}</p>
          </div>
        </div>
      </div>
      <div className="mb-3 mt-4 flex items-center justify-between text-xs">
        <span className="text-muted">{t('topology.port_distribution')}</span>
        <Link to="/topology" className="inline-flex items-center gap-1 text-accent">{t('overview.view_all')}<ArrowUpRight size={13} /></Link>
      </div>
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
        {ports.map(({ interface: port }) => {
          const relations = edges.filter(e => e.source_interface_id === port.id || e.label === port.name)
          const ids = new Set(relations.map(e => e.target_id))
          const clients = topology.data?.nodes.filter(n => ids.has(n.id)) ?? []
          const group = topology.data?.nodes.find(n => n.unresolved && n.children?.some(id => ids.has(id)))
          return (
            <div key={port.id} className="border-l-2 border-accent/35 bg-surface-2/60 px-3 py-3">
              <div className="flex items-center justify-between gap-2">
                <span className="inline-flex items-center gap-2 font-mono text-xs font-semibold"><EthernetPort size={14} />{port.name}</span>
                <span className="font-mono text-xs text-muted">{formatSpeed(port.speed_mbps)}</span>
              </div>
              <p className="mt-3 text-sm font-medium">{topology.isError ? '—' : t('topology.clients', { count: clients.length })}</p>
              <p className="mt-1 truncate text-[11px] text-muted" title={clients.map(n => n.device?.name).join(', ')}>
                {group ? t('topology.segment_hint') : clients.map(n => n.device?.name).slice(0, 2).join(' · ') || (port.carrier ? t('topology.select_node') : 'DOWN')}
              </p>
            </div>
          )
        })}
      </div>
      {topology.isError ? <p className="mt-3 text-xs text-warning">{t('app.error')} · {t('nav.topology')}</p> : null}
      {unplaced ? <p className="mt-4 text-xs text-muted">{t('topology.unmapped_count', { count: unplaced })}</p> : null}
    </div>
  )
}
