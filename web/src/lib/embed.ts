import { setSessionToken } from '@/lib/api'

import { t } from '@/i18n'

export function isEmbedded(pathname = window.location.pathname): boolean {
  return pathname === '/embed' || pathname.startsWith('/embed/')
}

/** Only the actual parent on this router may initialize an embedded session. */
export function trustedParentOrigin(referrer: string, ownOrigin: string): string | null {
  try {
    const parent = new URL(referrer)
    const own = new URL(ownOrigin)
    return /^https?:$/.test(parent.protocol) && parent.hostname === own.hostname ? parent.origin : null
  } catch {
    return null
  }
}

export function initializeEmbeddedSession(): Promise<void> {
  if (!isEmbedded()) return Promise.resolve()
  const origin = trustedParentOrigin(document.referrer, window.location.origin)
  if (window.parent === window || !origin) return Promise.reject(new Error(t('app.session_unavailable_title')))

  return new Promise((resolve, reject) => {
    const finish = (error?: Error) => {
      window.clearTimeout(timeout)
      window.removeEventListener('message', receive)
      if (error) reject(error)
      else resolve()
    }
    const receive = (event: MessageEvent) => {
      if (event.source !== window.parent || event.origin !== origin || event.data?.type !== 'lanory:session') return
      if (typeof event.data.token !== 'string' || !event.data.token) return
      setSessionToken(event.data.token)
      finish()
    }
    const timeout = window.setTimeout(() => finish(new Error(t('app.session_unavailable_body'))), 12_000)
    window.addEventListener('message', receive)
    window.parent.postMessage({ type: 'lanory:ready' }, origin)
  })
}

export function notifyParentRoute(path: string): void {
  if (!isEmbedded()) return
  const origin = trustedParentOrigin(document.referrer, window.location.origin)
  if (origin && window.parent !== window) window.parent.postMessage({ type: 'lanory:route', path }, origin)
}
