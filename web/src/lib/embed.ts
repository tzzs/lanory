import { setSessionToken } from '@/lib/api'
import { setTheme } from '@/lib/theme'
import { useUIStore } from '@/stores/ui'

import { t, type Language } from '@/i18n'

/**
 * Embedded entry (`/embed`): the app runs inside a LuCI iframe, and the parent
 * is the only party allowed to bootstrap it.
 *
 * Two messages come from the host:
 *
 *   lanory:session      the bearer credential, kept in memory (see api.ts)
 *   lanory:host-context the host's language and theme, applied as a default
 *
 * The host context follows one rule: **follow the host until the user
 * disagrees.** The host is a suggestion for a session that has no preference of
 * its own, never an override: once the user picks a language or a theme in
 * Lanory's own Settings, the host's value stops being applied.
 */

/** Preferences the LuCI host may supply for a session that has none of its own. */
export interface HostContext {
  lang?: Language
  theme?: 'light' | 'dark'
}

/** The UI store, as `zustand`'s `persist` writes it. */
const UI_STORAGE_KEY = 'lanory.ui'
/** What the host last supplied and this app actually took. */
const HOST_CONTEXT_KEY = 'lanory.host-context'

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

function isHostLanguage(value: unknown): value is Language {
  return value === 'zh-CN' || value === 'en'
}

function isHostTheme(value: unknown): value is 'light' | 'dark' {
  return value === 'light' || value === 'dark'
}

/** The persisted UI store, or null when it is absent or unusable. */
function persistedUI(): Record<string, unknown> | null {
  try {
    const parsed = JSON.parse(localStorage.getItem(UI_STORAGE_KEY) ?? 'null') as { state?: unknown } | null
    return parsed && typeof parsed.state === 'object' && parsed.state !== null ? (parsed.state as Record<string, unknown>) : null
  } catch {
    return null
  }
}

/** What the host last supplied and this app actually took. */
function hostApplied(): HostContext {
  try {
    const parsed = JSON.parse(localStorage.getItem(HOST_CONTEXT_KEY) ?? 'null') as { lang?: unknown; theme?: unknown } | null
    if (!parsed) return {}
    return {
      lang: isHostLanguage(parsed.lang) ? parsed.lang : undefined,
      theme: isHostTheme(parsed.theme) ? parsed.theme : undefined,
    }
  } catch {
    return {}
  }
}

/**
 * The value the store holds before anyone has chosen: the theme's own default,
 * and the language the i18n module seeds from the browser.
 *
 * `detectLanguage()` is deliberately not used for the language - it reads
 * `lanory.language`, which `setLanguage()` also writes when this module applies
 * a host value, so it reports the last applied value rather than the absence of
 * a choice.
 */
function storeDefault(field: 'theme' | 'language'): string {
  if (field === 'theme') return 'system'
  return typeof navigator !== 'undefined' && navigator.language?.toLowerCase().startsWith('zh') ? 'zh-CN' : 'en'
}

/**
 * Reports whether the user has already chosen `field` in Lanory's own Settings.
 *
 * The store persists to `lanory.ui`, so a value there looks like a choice - but
 * the store persists *everything* on every change, so answering the health poll
 * or selecting a device rewrites the theme and the language with the values they
 * already had. Presence alone would therefore report a choice after any
 * interaction, and the host would stop being followed on the next visit. A
 * stored value counts as a choice only when it is neither the store's default
 * nor the value this app last took from the host.
 */
function hasUserChosen(field: 'theme' | 'language'): boolean {
  const stored = persistedUI()?.[field]
  if (typeof stored !== 'string' || stored === '') return false
  const applied = hostApplied()
  const fromHost = field === 'theme' ? applied.theme : applied.lang
  return stored !== (fromHost ?? storeDefault(field))
}

/** Records the values this app took from the host, so they are not later mistaken for the user's own. */
function rememberHostContext(applied: HostContext): void {
  // Merge: the host may send one field only, and the other field's record has to
  // survive, otherwise a theme-only message would make the language look chosen.
  const record = hostApplied()
  if (applied.lang) record.lang = applied.lang
  if (applied.theme) record.theme = applied.theme
  try {
    localStorage.setItem(HOST_CONTEXT_KEY, JSON.stringify(record))
  } catch {
    /* ignore */
  }
}

/**
 * Applies the host's preferences, except where the user has already chosen.
 *
 * Both go through the store's setters - `setLang` routes through the i18n
 * module's `setLanguage`, and the theme needs the store's `setTheme` plus
 * `theme.ts`'s `setTheme`, exactly what the Settings page calls - rather than
 * writing localStorage here: the store is the single source of truth that
 * components render from and that gets persisted, and a direct write would be
 * overwritten by its next one. The theme's own setter also persists
 * `lanory.theme`, which the pre-paint script in index.html and `initTheme()`
 * read, so the value survives a reload instead of being undone on mount.
 */
function applyHostContext(context: HostContext): void {
  const applied: HostContext = {}
  if (context.lang && !hasUserChosen('language')) {
    useUIStore.getState().setLang(context.lang)
    applied.lang = context.lang
  }
  if (context.theme && !hasUserChosen('theme')) {
    useUIStore.getState().setTheme(context.theme)
    setTheme(context.theme)
    applied.theme = context.theme
  }
  if (applied.lang || applied.theme) rememberHostContext(applied)
}

let hostContextListener: ((event: MessageEvent) => void) | null = null

/**
 * Accepts host preferences for the lifetime of the page.
 *
 * The host posts this in answer to `lanory:ready`, but nothing orders it against
 * `lanory:session` - and the handshake removes its own listener the moment the
 * credential arrives - so this one is installed separately and stays. A previous
 * listener is dropped first so repeated bootstraps do not stack handlers.
 */
function listenForHostContext(origin: string): void {
  if (hostContextListener) window.removeEventListener('message', hostContextListener)
  hostContextListener = (event: MessageEvent) => {
    // The same defensive posture as the session handshake: this must still be the
    // embedded entry, the message must come from the actual parent on this router,
    // and every field is validated. An unrecognised value is ignored, not applied.
    if (!isEmbedded()) return
    if (event.source !== window.parent || event.origin !== origin) return
    const data: unknown = event.data
    if (typeof data !== 'object' || data === null) return
    const message = data as { type?: unknown; lang?: unknown; theme?: unknown }
    if (message.type !== 'lanory:host-context') return
    const context: HostContext = {}
    if (isHostLanguage(message.lang)) context.lang = message.lang
    if (isHostTheme(message.theme)) context.theme = message.theme
    applyHostContext(context)
  }
  window.addEventListener('message', hostContextListener)
}

export function initializeEmbeddedSession(): Promise<void> {
  if (!isEmbedded()) return Promise.resolve()
  const origin = trustedParentOrigin(document.referrer, window.location.origin)
  if (window.parent === window || !origin) return Promise.reject(new Error(t('app.session_unavailable_title')))

  listenForHostContext(origin)

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
