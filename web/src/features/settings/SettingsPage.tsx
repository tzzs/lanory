import { useEffect, useState } from 'react'
import { Moon, Sun, MonitorCog } from 'lucide-react'
import { t } from '@/i18n'
import { useSettings, usePatchSettings, useDiscoveryStatus, useHealth } from '@/hooks/useApi'
import { useUIStore } from '@/stores/ui'
import { setTheme, type ThemeMode } from '@/lib/theme'
import { Card, CardHeader, CardBody, Button, Input, Label, Checkbox, Badge, Skeleton, FieldRow, ErrorState, PageHeader, SectionHeader } from '@/components/ui'
import { formatBytes, formatRelativeTime } from '@/lib/devices'

const THEME_OPTIONS: { value: ThemeMode; label: string; icon: typeof Sun }[] = [
  { value: 'light', label: 'settings.theme_light', icon: Sun },
  { value: 'dark', label: 'settings.theme_dark', icon: Moon },
  { value: 'system', label: 'settings.theme_system', icon: MonitorCog },
]

export function SettingsPage() {
  const theme = useUIStore((s) => s.theme)
  const setThemeStore = useUIStore((s) => s.setTheme)
  const language = useUIStore((s) => s.language)
  const setLang = useUIStore((s) => s.setLang)

  const settings = useSettings()
  const patch = usePatchSettings()
  const health = useHealth()
  const discovery = useDiscoveryStatus()

  const [values, setValues] = useState<Record<string, string>>({})

  useEffect(() => {
    if (settings.data?.settings) setValues(settings.data.settings)
  }, [settings.data])

  const dirty = Object.keys(values).some((key) => values[key] !== settings.data?.settings?.[key])

  function set(key: string, value: string) {
    setValues((current) => ({ ...current, [key]: value }))
  }

  function setBool(key: string, value: boolean) {
    set(key, value ? 'true' : 'false')
  }

  if (settings.isError) {
    return (
      <ErrorState
        title={t('app.error')}
        description={settings.error instanceof Error ? settings.error.message : undefined}
        action={
          <Button variant="primary" onClick={() => void settings.refetch()}>
            {t('app.retry')}
          </Button>
        }
      />
    )
  }

  if (settings.isLoading || Object.keys(values).length === 0) {
    return (
      <div className="space-y-4 p-6">
        <Skeleton className="h-8 w-40" />
        <Skeleton className="h-48" />
        <Skeleton className="h-48" />
      </div>
    )
  }

  const bool = (key: string, fallback = false) => values[key] ? values[key] === 'true' : fallback
  const num = (key: string, fallback = 0) => {
    const parsed = Number(values[key])
    return Number.isFinite(parsed) ? parsed : fallback
  }

  return (
    <div className="space-y-5 p-6">
      <PageHeader title={t('settings.title')} description={t('settings.subtitle')} />

      {/* appearance */}
      <Card>
        <CardHeader>
          <SectionHeader title={t('settings.appearance')} />
        </CardHeader>
        <CardBody className="space-y-4">
          <div>
            <p className="mb-2 text-xs text-muted">{t('settings.theme')}</p>
            <div className="flex gap-2">
              {THEME_OPTIONS.map(({ value, label, icon: Icon }) => (
                <Button
                  key={value}
                  size="sm"
                  variant={theme === value ? 'primary' : 'secondary'}
                  onClick={() => {
                    setThemeStore(value)
                    setTheme(value)
                  }}
                >
                  <Icon size={14} aria-hidden="true" />
                  {t(label)}
                </Button>
              ))}
            </div>
          </div>

          <div>
            <p className="mb-2 text-xs text-muted">{t('settings.language')}</p>
            <div className="flex gap-2">
              <Button size="sm" variant={language === 'zh-CN' ? 'primary' : 'secondary'} onClick={() => setLang('zh-CN')}>
                简体中文
              </Button>
              <Button size="sm" variant={language === 'en' ? 'primary' : 'secondary'} onClick={() => setLang('en')}>
                English
              </Button>
            </div>
          </div>
        </CardBody>
      </Card>

      {/* discovery */}
      <Card>
        <CardHeader>
          <SectionHeader
            title={t('settings.discovery')}
            actions={
              discovery.data ? (
                <Badge tone={discovery.data.enabled ? 'success' : 'neutral'}>
                  {discovery.data.enabled ? t('discovery.enabled') : t('app.no')}
                </Badge>
              ) : null
            }
          />
        </CardHeader>
        <CardBody className="space-y-3">
          <Checkbox
            label={t('settings.discovery_enabled')}
            checked={bool('discovery.enabled', true)}
            onChange={(e) => setBool('discovery.enabled', e.target.checked)}
          />
          {/* Stacked label above the control: the label length must not decide
              where the input starts, or two fields in one card misalign. */}
          <div>
            <Label htmlFor="settings-discovery-interval">{t('settings.discovery_interval')}</Label>
            <Input
              id="settings-discovery-interval"
              className="max-w-[200px] font-mono"
              value={values['discovery.interval'] ?? '10m'}
              onChange={(e) => set('discovery.interval', e.target.value)}
              placeholder="10m"
            />
          </div>
          <div>
            <Label htmlFor="settings-retention">{t('settings.retention')}</Label>
            <Input
              id="settings-retention"
              className="max-w-[200px] font-mono"
              type="number"
              min={1}
              max={365}
              value={values['database.retention_days'] ?? '30'}
              onChange={(e) => set('database.retention_days', e.target.value)}
            />
          </div>

          {discovery.data?.last_job ? (
            <dl className="divide-y divide-border border-t border-border pt-2">
              <FieldRow label={t('discovery.last_run')}>
                {discovery.data.last_job.finished_at
                  ? formatRelativeTime(discovery.data.last_job.finished_at)
                  : t('overview.never_scanned')}
              </FieldRow>
              <FieldRow label={t('discovery.devices_found')}>{discovery.data.last_job.devices_found}</FieldRow>
              <FieldRow label={t('discovery.interfaces_found')}>{discovery.data.last_job.interfaces_found}</FieldRow>
              <FieldRow label={t('discovery.edges_created')}>{discovery.data.last_job.edges_created}</FieldRow>
            </dl>
          ) : null}
        </CardBody>
      </Card>

      {/* collectors */}
      {discovery.data?.collectors && discovery.data.collectors.length > 0 ? (
        <Card>
          <CardHeader>
            <SectionHeader title={t('settings.collectors')} />
          </CardHeader>
          <CardBody>
            <ul className="divide-y divide-border">
              {discovery.data.collectors.map((collector) => (
                <li key={collector.name} className="flex items-center justify-between gap-3 py-2">
                  <div className="min-w-0">
                    <p className="font-mono text-sm text-fg">{collector.name}</p>
                    {collector.description ? (
                      <p className="truncate text-[11px] text-muted">{collector.description}</p>
                    ) : null}
                  </div>
                  <div className="flex shrink-0 items-center gap-2">
                    {collector.last_duration_ms ? (
                      <span className="text-[11px] text-muted">{collector.last_duration_ms} ms</span>
                    ) : null}
                    <Badge tone={collector.supported ? 'success' : 'neutral'}>
                      {collector.supported ? t('discovery.supported') : t('discovery.unsupported')}
                    </Badge>
                  </div>
                </li>
              ))}
            </ul>
          </CardBody>
        </Card>
      ) : null}

      {/* monitoring */}
      <Card>
        <CardHeader>
          <SectionHeader title={t('settings.monitoring')} />
        </CardHeader>
        <CardBody className="space-y-3">
          <Checkbox
            label={t('settings.monitoring_enabled')}
            checked={bool('monitoring.enabled', true)}
            onChange={(e) => setBool('monitoring.enabled', e.target.checked)}
          />
          <div className="grid gap-4 sm:grid-cols-3">
            <div>
              <Label htmlFor="settings-monitoring-interval">{t('settings.monitoring_interval')}</Label>
              <Input
                id="settings-monitoring-interval"
                className="font-mono"
                value={values['monitoring.interval'] ?? '60s'}
                onChange={(e) => set('monitoring.interval', e.target.value)}
                placeholder="60s"
              />
            </div>
            <div>
              <Label htmlFor="settings-failure-threshold">{t('settings.failure_threshold')}</Label>
              <Input
                id="settings-failure-threshold"
                className="font-mono"
                type="number"
                min={1}
                max={20}
                value={values['monitoring.failure_threshold'] ?? '3'}
                onChange={(e) => set('monitoring.failure_threshold', e.target.value)}
              />
            </div>
            <div>
              <Label htmlFor="settings-monitoring-timeout">
                {t('monitoring.probe_timeout' in values ? 'monitoring.probe_timeout' : 'settings.monitoring_interval')}
              </Label>
              <Input
                id="settings-monitoring-timeout"
                className="font-mono"
                value={values['monitoring.timeout'] ?? '3s'}
                onChange={(e) => set('monitoring.timeout', e.target.value)}
                placeholder="3s"
              />
            </div>
          </div>
        </CardBody>
      </Card>

      {/* topology */}
      <Card>
        <CardHeader>
          <SectionHeader title={t('settings.topology')} />
        </CardHeader>
        <CardBody className="space-y-3">
          <Checkbox
            label={t('settings.auto_layout')}
            checked={bool('topology.auto_layout', true)}
            onChange={(e) => setBool('topology.auto_layout', e.target.checked)}
          />
          <Checkbox
            label={t('settings.preserve_layout')}
            checked={bool('topology.preserve_manual_layout', true)}
            onChange={(e) => setBool('topology.preserve_manual_layout', e.target.checked)}
          />
        </CardBody>
      </Card>

      {/* security */}
      <Card>
        <CardHeader>
          <SectionHeader title={t('settings.security')} />
        </CardHeader>
        <CardBody className="space-y-3">
          <Checkbox
            label={t('settings.auth_token')}
            checked={bool('auth.enabled', true)}
            onChange={(e) => setBool('auth.enabled', e.target.checked)}
          />
          <p className="text-[11px] text-muted">{t('settings.auth_note')}</p>
          <p className="rounded-md border border-border bg-surface-2 px-3 py-2 text-[11px] text-muted">
            {t('settings.readonly_note')}
          </p>
        </CardBody>
      </Card>

      {/* about */}
      <Card>
        <CardHeader>
          <SectionHeader title={t('settings.about')} />
        </CardHeader>
        <CardBody>
          <dl className="divide-y divide-border">
            <FieldRow label={t('settings.version')}>{health.data?.version ?? '—'}</FieldRow>
            <FieldRow label={t('settings.db_schema')}>{values['database.schema_version'] ?? '—'}</FieldRow>
            <FieldRow label={t('settings.db_size')}>
              {num('database.size_bytes') ? formatBytes(num('database.size_bytes')) : '—'}
            </FieldRow>
            <FieldRow label={t('overview.total_devices')}>{num('devices.total') || '—'}</FieldRow>
          </dl>
        </CardBody>
      </Card>

      {/* save bar */}
      <div className="sticky bottom-0 flex items-center justify-end gap-2 border-t border-border bg-surface/95 py-3 backdrop-blur">
        {patch.isSuccess ? <span className="text-xs text-success">{t('settings.saved')}</span> : null}
        {patch.isError ? <span className="text-xs text-danger">{t('settings.save_failed')}</span> : null}
        <Button
          variant="ghost"
          onClick={() => setValues(settings.data?.settings ?? {})}
          disabled={!dirty}
        >
          {t('app.cancel')}
        </Button>
        <Button
          variant="primary"
          disabled={!dirty || patch.isPending}
          onClick={() => patch.mutate(values as Record<string, string>)}
        >
          {patch.isPending ? t('app.loading') : t('app.save')}
        </Button>
      </div>
    </div>
  )
}
