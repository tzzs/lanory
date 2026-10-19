import { Lock, LockOpen, Trash2 } from 'lucide-react'
import { t } from '@/i18n'
import { usePatchEdge, useDeleteEdge } from '@/hooks/useApi'
import { useUIStore } from '@/stores/ui'
import { Button, Card, CardHeader, CardTitle, Badge, FieldRow } from '@/components/ui'
import { formatConfidence } from '@/lib/devices'
import type { Edge } from '@xyflow/react'
import type { TopologyEdgeData } from '@/features/topology/graph'

/**
 * Inspector for a selected edge.
 *
 * Shows the connection type, port, speed, state and confidence, plus the
 * evidence count so the user can tell an inference from a confirmed fact.
 */
export function EdgeInspector({ edge }: { edge: Edge<TopologyEdgeData> }) {
  const data = edge.data
  const patch = usePatchEdge()
  const remove = useDeleteEdge()
  const selectEdge = useUIStore((s) => s.selectEdge)
  const setDetailPanelOpen = useUIStore((s) => s.setDetailPanelOpen)

  if (!data) return null

  const statusLabel: Record<string, string> = {
    link_up: t('event.link.up'),
    link_down: t('event.link.down'),
    reachable: t('state.online'),
    unreachable: t('state.offline'),
    unverified: t('topology.unverified'),
  }

  return (
    <aside className="flex w-80 shrink-0 flex-col border-l border-border bg-surface">
      <CardHeader>
        <CardTitle>{t('topology.edge_details')}</CardTitle>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => {
            selectEdge(null)
            setDetailPanelOpen(false)
          }}
        >
          {t('app.close')}
        </Button>
      </CardHeader>

      <div className="min-h-0 flex-1 space-y-5 overflow-y-auto p-4">
        <dl className="divide-y divide-border">
          <FieldRow label={t('device.edges')}>{t(`edge.${data.edgeType}`)}</FieldRow>
          <FieldRow label={t('device.interface')} mono>
            {data.port || '—'}
          </FieldRow>
          <FieldRow label={t('device.link_speed')}>
            {data.speedMbps ? `${data.speedMbps} Mbps` : '—'}
          </FieldRow>
          <FieldRow label={t('monitoring.port_state')}>{statusLabel[data.status] ?? data.status}</FieldRow>
          <FieldRow label={t('topology.confidence')}>{formatConfidence(data.confidence)}</FieldRow>
          <FieldRow label={t('topology.evidence')}>{data.evidenceCount}</FieldRow>
        </dl>

        <div className="flex flex-wrap gap-1.5">
          {data.userConfirmed ? <Badge tone="success">{t('topology.confirmed')}</Badge> : null}
          {data.locked ? <Badge>{t('topology.locked')}</Badge> : null}
          {data.unverified ? <Badge tone="warning">{t('topology.unverified')}</Badge> : null}
        </div>

        {data.unverified ? <p className="text-[11px] text-muted">{t('topology.inferred_hint')}</p> : null}
        {data.locked ? <p className="text-[11px] text-muted">{t('topology.locked_edge_hint')}</p> : null}

        {/* Confidence explanation: which sources contributed. */}
        <section>
          <h4 className="mb-1.5 text-[11px] font-semibold uppercase tracking-wider text-muted">
            {t('topology.evidence')}
          </h4>
          <p className="text-[11px] text-muted">
            {data.evidenceCount > 0
              ? `${data.evidenceCount} ${t('topology.evidence')}`
              : t('topology.inferred_hint')}
          </p>
        </section>
      </div>

      <div className="flex items-center gap-2 border-t border-border p-3">
        <Button
          size="sm"
          disabled={patch.isPending}
          onClick={() => {
            patch.mutate({ id: data.edgeId, body: { locked: !data.locked, user_confirmed: true } })
          }}
        >
          {data.locked ? <LockOpen size={14} aria-hidden="true" /> : <Lock size={14} aria-hidden="true" />}
          {data.locked ? t('topology.unlock') : t('topology.lock')}
        </Button>
        <Button
          size="sm"
          variant="ghost"
          className="ml-auto text-danger"
          disabled={data.locked || remove.isPending}
          title={data.locked ? t('topology.locked_edge_hint') : undefined}
          onClick={() => {
            if (!window.confirm(t('topology.delete_edge_confirm'))) return
            remove.mutate(data.edgeId, {
              onSuccess: () => {
                selectEdge(null)
                setDetailPanelOpen(false)
              },
            })
          }}
        >
          <Trash2 size={14} aria-hidden="true" />
          {t('topology.delete_edge')}
        </Button>
      </div>
    </aside>
  )
}

/** Placeholder shown when nothing is selected, mirroring the device panel. */
export function DeviceInspectorPlaceholder() {
  return (
    <aside className="hidden w-80 shrink-0 border-l border-border bg-surface lg:block">
      <div className="p-4">
        <p className="text-xs text-muted">{t('topology.select_node')}</p>
      </div>
    </aside>
  )
}

export { Card }
