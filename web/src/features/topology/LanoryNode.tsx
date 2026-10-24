import { memo } from 'react'
import { Handle, Position, type NodeProps } from '@xyflow/react'
import {
  Globe,
  Router,
  Network,
  Wifi,
  Monitor,
  Laptop,
  Smartphone,
  Server,
  HardDrive,
  Cable,
  HelpCircle,
  CircleDashed,
  Box,
  type LucideIcon,
} from 'lucide-react'
import { cn } from '@/lib/cn'
import { t } from '@/i18n'
import type { TopologyNodeData } from '@/features/topology/graph'

const ICONS: Record<string, LucideIcon> = {
  globe: Globe,
  router: Router,
  network: Network,
  wifi: Wifi,
  monitor: Monitor,
  laptop: Laptop,
  smartphone: Smartphone,
  server: Server,
  'hard-drive': HardDrive,
  cable: Cable,
  generic: Box,
  unresolved: CircleDashed,
}

const STATE_RING: Record<string, string> = {
  online: 'ring-success/50',
  offline: 'ring-danger/50',
  sleeping: 'ring-warning/50',
  unknown: 'ring-border',
}

const STATE_DOT: Record<string, string> = {
  online: 'bg-success',
  offline: 'bg-danger',
  sleeping: 'bg-warning',
  unknown: 'bg-muted',
}

/**
 * A topology node. Shows icon, name, primary IP and online state — no more,
 * per the spec's "device card should not be overly complex" requirement.
 * Details live in the right-hand panel.
 */
function LanoryNodeComponent({ data, selected }: NodeProps) {
  const nodeData = data as TopologyNodeData
  const Icon = ICONS[nodeData.icon] ?? (nodeData.kind === 'unresolved' ? CircleDashed : HelpCircle)

  if (nodeData.annotation) return <div className="text-sm font-semibold text-muted">{nodeData.label}</div>

  if (nodeData.group) return (
    <div className="h-full w-full rounded-xl border border-dashed border-muted/50 bg-surface-2/60">
      <Handle type="target" position={Position.Top} className="!bg-muted" />
      <div className="flex items-start justify-between gap-6 border-b border-dashed border-border px-6 py-4">
        <div>
          <p className="text-sm font-semibold text-fg">{nodeData.label}</p>
          <p className="mt-1 text-xs text-muted">{nodeData.subtitle}</p>
        </div>
        <span className="shrink-0 rounded-md border border-border bg-surface px-2 py-1 text-xs text-fg">{t('topology.clients', { count: nodeData.memberCount ?? 0 })}</span>
      </div>
    </div>
  )

  return (
    <div
      className={cn(
        'group relative w-[200px] rounded-lg border border-border bg-surface px-3 py-2.5 shadow-sm transition-shadow',
        'hover:shadow-md',
        selected && 'ring-2 ring-accent',
        nodeData.kind !== 'unresolved' && `ring-1 ${STATE_RING[nodeData.state] ?? 'ring-border'}`,
        nodeData.kind === 'unresolved' && 'border-dashed',
      )}
    >
      <Handle type="target" position={Position.Top} className="!bg-muted" />

      <div className="flex items-start gap-2.5">
        <div
          className={cn(
            'flex h-8 w-8 shrink-0 items-center justify-center rounded-md',
            nodeData.kind === 'unresolved' ? 'bg-surface-2' : 'bg-accent/10',
          )}
        >
          <Icon size={16} className="text-muted" aria-hidden="true" />
        </div>

        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5">
            <span title={nodeData.label} className="truncate text-[13px] font-medium text-fg">{nodeData.label}</span>
            {nodeData.isManual ? (
              <span className="shrink-0 rounded bg-accent/15 px-1 text-[9px] font-medium text-accent">
                {t('devices.manual_badge')}
              </span>
            ) : null}
          </div>
          <p className="truncate font-mono text-[10px] text-muted">
            {nodeData.subtitle || nodeData.primaryIp || nodeData.primaryMac || t('app.unknown')}
          </p>
        </div>
      </div>

      <div className="mt-1.5 flex items-center gap-1.5">
        <span
          className={cn('inline-block h-1.5 w-1.5 rounded-full', STATE_DOT[nodeData.state] ?? 'bg-muted')}
          aria-hidden="true"
        />
        <span className="text-[10px] text-muted">{t(`state.${nodeData.state}`)}</span>
        {nodeData.isLocked ? (
          <span className="ml-auto text-[9px] text-muted">{t('topology.locked')}</span>
        ) : null}
      </div>

      {/* Screen-reader summary: state and address as text, not colour only. */}
      <span className="sr-only">
        {nodeData.label}, {t(`state.${nodeData.state}`)}, {nodeData.primaryIp ?? t('app.unknown')}
      </span>

      <Handle type="source" position={Position.Bottom} className="!bg-muted" />
    </div>
  )
}

export const LanoryNode = memo(LanoryNodeComponent)
