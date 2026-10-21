import { useCallback, useEffect, useState } from 'react'
import { KeyRound, LogIn } from 'lucide-react'
import { t } from '@/i18n'
import { readToken, writeToken } from '@/lib/api'
import { Button, Card, CardBody, CardHeader, CardTitle, Input } from '@/components/ui'

/**
 * Token prompt.
 *
 * The API requires a bearer token, which the OpenWrt init script generates on
 * first start and writes to /etc/lanory/token (mode 0600). Without a way to
 * enter it the page renders empty and gives the user nothing to act on: the
 * requests fail with 401 and nothing in the UI mentions a token at all.
 *
 * This component is shown whenever the API reports "unauthorized" - on the very
 * first visit, and again whenever a stored token stops being accepted (the file
 * was regenerated, or the service was reinstalled).
 */
export function TokenPrompt({ onAuthenticated }: { onAuthenticated?: () => void }) {
  const [value, setValue] = useState("")
  const [error, setError] = useState<string | null>(null)

  const submit = useCallback(() => {
    const token = value.trim()
    if (token === "") {
      setError(t('token.empty'))
      return
    }
    writeToken(token)
    setError(null)
    onAuthenticated?.()
  }, [value, onAuthenticated])

  return (
    <div className="flex min-h-full items-center justify-center p-6">
      <Card className="w-full max-w-md">
        <CardHeader>
          <div className="flex items-center gap-2.5">
            <div className="flex h-9 w-9 items-center justify-center rounded-md bg-accent/12">
              <KeyRound size={17} className="text-accent" aria-hidden="true" />
            </div>
            <div>
              <CardTitle>{t('token.title')}</CardTitle>
              <p className="mt-0.5 text-xs text-muted">{t('token.subtitle')}</p>
            </div>
          </div>
        </CardHeader>
        <CardBody className="space-y-4">
          <form
            className="space-y-3"
            onSubmit={(e) => {
              e.preventDefault()
              submit()
            }}
          >
            <label className="block">
              <span className="text-xs text-muted">{t('token.label')}</span>
              <Input
                className="mt-1 font-mono"
                type="password"
                autoComplete="current-password"
                autoFocus
                value={value}
                onChange={(e) => setValue(e.target.value)}
                placeholder={t('token.placeholder')}
              />
            </label>
            {error ? (
              <p role="alert" className="text-xs text-danger">
                {error}
              </p>
            ) : null}
            <Button type="submit" variant="primary" className="w-full">
              <LogIn size={14} aria-hidden="true" />
              {t('token.submit')}
            </Button>
          </form>

          <div className="rounded-md border border-border bg-surface-2 px-3 py-2.5 text-xs text-muted">
            <p className="font-medium text-fg">{t('token.how_title')}</p>
            <p className="mt-1">{t('token.how_body')}</p>
            <code className="mt-2 block overflow-x-auto whitespace-pre rounded bg-bg px-2 py-1.5 font-mono text-[11px] text-fg">
              {t('token.how_command')}
            </code>
            <p className="mt-2">{t('token.how_note')}</p>
          </div>

          {readToken() ? (
            <Button
              variant="ghost"
              size="sm"
              className="w-full"
              onClick={() => {
                writeToken(null)
                onAuthenticated?.()
              }}
            >
              {t('token.clear')}
            </Button>
          ) : null}
        </CardBody>
      </Card>
    </div>
  )
}

/**
 * useTokenGate tracks whether the API has rejected the current token.
 *
 * The gate is driven by the API itself rather than by a guess: the app calls the
 * health endpoint on mount, and only shows the prompt when the API says
 * "unauthorized". A deployment with authentication switched off therefore never
 * sees the prompt at all.
 */
export function useTokenGate() {
  const [required, setRequired] = useState(false)
  const [checked, setChecked] = useState(false)

  const recheck = useCallback(async () => {
    try {
      const res = await fetch('/api/v1/health', { cache: 'no-store' })
      if (res.status === 401) {
        setRequired(true)
      } else {
        setRequired(false)
      }
    } catch {
      // The API is unreachable; do not claim a token is needed.
      setRequired(false)
    } finally {
      setChecked(true)
    }
  }, [])

  useEffect(() => {
    void recheck()
  }, [recheck])

  return { required, checked, recheck }
}
