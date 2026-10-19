import { useState } from 'react'
import { Link } from 'react-router-dom'
import { t } from '@/i18n'
import { useEvents } from '@/hooks/useApi'
import { Card, Button, Select, Input, Badge, Skeleton, EmptyState, ErrorState, Table, THead, TBody, TR, TH, TD } from '@/components/ui'
import { formatRelativeTime, formatTimestamp } from '@/lib/devices'
import type { EventType } from '@/types/api'

const EVENT_TYPES: EventType[] = [
  'device.online',
  'device.offline',
  'device.discovered',
  'device.removed',
  'device.ip_changed',
  'device.mac_changed',
  'link.up',
  'link.down',
  'speed.changed',
  'topology.changed',
  'probe.completed',
  'discovery.started',
  'discovery.completed',
  'device.merged',
  'device.hidden',
]

const PAGE_SIZE = 100

export function EventsPage() {
  const [type, setType] = useState('')
  const [deviceId, setDeviceId] = useState('')
  const [since, setSince] = useState('')
  const [query, setQuery] = useState('')
  const [limit, setLimit] = useState(PAGE_SIZE)

  const { data, isLoading, isError, error, refetch } = useEvents({
    type: type || undefined,
    device_id: deviceId || undefined,
    since: since || undefined,
    limit,
  })

  const total = data?.total ?? 0
  const hasMore = total > (data?.events.length ?? 0)

  return (
    <div className="space-y-4 p-6">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h2 className="text-lg font-semibold text-fg">{t('events.title')}</h2>
          <p className="text-xs text-muted">{t('events.total', { count: total })}</p>
        </div>
        <Button size="sm" variant="ghost" onClick={() => void refetch()}>
          {t('app.refresh')}
        </Button>
      </div>

      <Card className="p-3">
        <div className="flex flex-wrap items-center gap-2">
          <Select
            className="w-auto min-w-[150px]"
            value={type}
            onChange={(e) => setType(e.target.value)}
            aria-label={t('events.filter_type')}
          >
            <option value="">{t('events.filter_type')}</option>
            {EVENT_TYPES.map((eventType) => (
              <option key={eventType} value={eventType}>
                {t(`event.${eventType}`)}
              </option>
            ))}
          </Select>

          <Input
            className="w-auto min-w-[200px]"
            placeholder={t('events.filter_device')}
            value={deviceId}
            onChange={(e) => setDeviceId(e.target.value)}
            aria-label={t('events.filter_device')}
          />

          <Input
            className="w-auto min-w-[180px]"
            type="datetime-local"
            value={since}
            onChange={(e) => setSince(e.target.value ? new Date(e.target.value).toISOString() : '')}
            aria-label={t('events.filter_since')}
          />

          <Input
            className="min-w-[200px] flex-1"
            placeholder={t('app.search')}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            aria-label={t('app.search')}
          />
        </div>
      </Card>

      <Card className="overflow-hidden">
        {isLoading ? (
          <div className="space-y-2 p-4">
            {Array.from({ length: 10 }).map((_, i) => (
              <Skeleton key={i} className="h-9" />
            ))}
          </div>
        ) : isError ? (
          <ErrorState
            title={t('app.error')}
            description={error instanceof Error ? error.message : undefined}
            action={
              <Button variant="primary" onClick={() => void refetch()}>
                {t('app.retry')}
              </Button>
            }
          />
        ) : !data || data.events.length === 0 ? (
          <EmptyState title={t('events.empty')} description={t('events.subtitle')} />
        ) : (
          <>
            <Table>
              <THead>
                <TR className="hover:bg-transparent">
                  <TH className="w-44">{t('events.col_time')}</TH>
                  <TH className="w-40">{t('events.col_type')}</TH>
                  <TH className="w-48">{t('events.col_device')}</TH>
                  <TH>{t('events.col_message')}</TH>
                </TR>
              </THead>
              <TBody>
                {data.events.map((event) => (
                  <TR key={event.id}>
                    <TD className="text-xs text-muted" title={formatTimestamp(event.timestamp)}>
                      {formatRelativeTime(event.timestamp)}
                    </TD>
                    <TD>
                      <Badge>{t(`event.${event.event_type}`)}</Badge>
                    </TD>
                    <TD className="text-xs">
                      {event.device_id ? (
                        <Link to={`/devices/${event.device_id}`} className="font-mono text-accent hover:underline">
                          {event.device_id}
                        </Link>
                      ) : (
                        '—'
                      )}
                    </TD>
                    <TD className="text-xs">
                      {event.message || '—'}
                      {event.old_value && event.new_value ? (
                        <span className="ml-1.5 font-mono text-muted">
                          {event.old_value} → {event.new_value}
                        </span>
                      ) : null}
                    </TD>
                  </TR>
                ))}
              </TBody>
            </Table>

            {hasMore ? (
              <div className="flex justify-center border-t border-border p-3">
                <Button size="sm" variant="ghost" onClick={() => setLimit(limit + PAGE_SIZE)}>
                  {t('overview.view_all')} ({limit + PAGE_SIZE})
                </Button>
              </div>
            ) : null}
          </>
        )}
      </Card>
    </div>
  )
}
