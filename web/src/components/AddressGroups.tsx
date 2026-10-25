/**
 * Renders a device's addresses grouped by subnet and side.
 *
 * The device detail endpoints return a flat list of addresses, which loses the
 * one thing a multi-homed box needs to show: 192.168.100.1 and 192.168.1.4 are
 * the same device, but they are the LAN and the WAN side of it, and a comma
 * separated list makes that impossible to read.
 *
 * A single-subnet device still renders as a plain list — grouping only appears
 * when there is something to distinguish.
 */

import { t } from '@/i18n'
import { cn } from '@/lib/cn'
import type { AddressGroup } from '@/lib/devices'

/** Label for a group: the side when known, else the subnet, else unknown. */
function groupLabel(group: AddressGroup): string {
  if (group.side && group.side !== 'unknown') return t(`role.${group.side}`)
  if (group.cidr) return group.cidr
  return t('app.unknown')
}

export function AddressGroups({
  groups,
  fallback,
  className,
}: {
  groups: AddressGroup[]
  /** Rendered when there are no addresses at all. */
  fallback?: string
  className?: string
}) {
  if (groups.length === 0) {
    return <span className="text-muted">{fallback ?? '—'}</span>
  }

  if (groups.length === 1) {
    return <span className={cn('font-mono', className)}>{groups[0].addresses.map((a) => a.address).join(', ')}</span>
  }

  return (
    <span className={cn('flex flex-col items-end gap-1', className)}>
      {groups.map((group) => (
        <span
          key={`${group.cidr}|${group.side}|${group.addresses[0]?.address ?? ''}`}
          className="flex flex-col items-end"
        >
          <span className="text-[10px] uppercase tracking-wide text-muted">
            {groupLabel(group)}
            {group.cidr && group.side !== 'unknown' ? ` · ${group.cidr}` : ''}
          </span>
          <span className="font-mono">{group.addresses.map((a) => a.address).join(', ')}</span>
        </span>
      ))}
    </span>
  )
}
