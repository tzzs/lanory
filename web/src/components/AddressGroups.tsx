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

import { useState } from 'react'
import { t } from '@/i18n'
import { cn } from '@/lib/cn'
import type { AddressGroup } from '@/lib/devices'

const DEFAULT_VISIBLE_ADDRESSES = 2

/** Label for a group when its network side is known. */
function groupLabel(group: AddressGroup): string | undefined {
  if (group.side && group.side !== 'unknown') return t(`role.${group.side}`)
  if (group.cidr) return group.cidr
  return undefined
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
  const [expanded, setExpanded] = useState(false)
  const addresses = groups.flatMap((group) => group.addresses)
  const visibleAddresses = expanded ? addresses : addresses.slice(0, DEFAULT_VISIBLE_ADDRESSES)
  const hiddenCount = addresses.length - visibleAddresses.length

  if (groups.length === 0) {
    return <span className="text-muted">{fallback ?? '—'}</span>
  }

  return (
    <span className={cn('flex min-w-0 flex-col items-end gap-1', className)}>
      {groups.map((group) => {
        const groupAddresses = group.addresses.filter((address) => visibleAddresses.includes(address))
        if (groupAddresses.length === 0) return null
        return (
          <span
            key={`${group.cidr}|${group.side}|${group.addresses[0]?.address ?? ''}`}
            className="flex max-w-full flex-col items-end gap-0.5"
          >
            {groups.length > 1 && groupLabel(group) ? (
              <span className="text-[10px] uppercase tracking-wide text-muted">
                {groupLabel(group)}
                {group.cidr && group.side !== 'unknown' ? ` · ${group.cidr}` : ''}
              </span>
            ) : null}
            {groupAddresses.map((address) => (
              <span
                key={address.address}
                className="max-w-full overflow-x-auto whitespace-nowrap text-right font-mono"
                title={address.address}
              >
                {address.address}
              </span>
            ))}
          </span>
        )
      })}
      {hiddenCount > 0 || expanded ? (
        <button
          type="button"
          className="text-[11px] text-accent hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/50"
          onClick={() => setExpanded((value) => !value)}
        >
          {expanded ? t('device.show_fewer_addresses') : t('device.show_all_addresses', { count: addresses.length })}
        </button>
      ) : null}
    </span>
  )
}
