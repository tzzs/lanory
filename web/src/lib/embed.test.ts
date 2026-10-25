import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { isEmbedded, trustedParentOrigin, notifyParentRoute, initializeEmbeddedSession } from './embed'
import { readToken, setSessionToken, writeToken } from './api'
import { useUIStore } from '@/stores/ui'
import { getLanguage, setLanguage } from '@/i18n'
import { getTheme, setTheme } from './theme'

// The store is a module singleton, so its state has to be put back between
// tests; `localStorage.clear()` alone leaves the previous test's values in it.
beforeEach(() => {
  setLanguage('en')
  setTheme('system')
  useUIStore.setState({ theme: 'system', language: 'en' })
})

afterEach(() => { setSessionToken(null); writeToken(null); vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.useRealTimers(); localStorage.clear(); history.replaceState(null, '', '/') })

describe('embedded entry', () => {
  it('only recognizes the dedicated embed route', () => {
    expect(isEmbedded('/embed')).toBe(true)
    expect(isEmbedded('/embed/devices/device-a')).toBe(true)
    expect(isEmbedded('/embedded')).toBe(false)
    expect(isEmbedded('/topology')).toBe(false)
  })

  it('trusts only HTTP parents on the same router, including IPv6', () => {
    expect(trustedParentOrigin('http://192.168.100.1/cgi-bin/luci/', 'http://192.168.100.1:8787')).toBe('http://192.168.100.1')
    expect(trustedParentOrigin('https://[fd00::1]/cgi-bin/luci/', 'http://[fd00::1]:8787')).toBe('https://[fd00::1]')
    expect(trustedParentOrigin('https://evil.example', 'http://192.168.100.1:8787')).toBeNull()
    expect(trustedParentOrigin('javascript:alert(1)', 'http://192.168.100.1:8787')).toBeNull()
    expect(trustedParentOrigin('', 'http://192.168.100.1:8787')).toBeNull()
  })

  it('keeps LuCI credentials in memory without replacing independent login', () => {
    writeToken('standalone-token')
    setSessionToken('luci-session-token')
    expect(readToken()).toBe('luci-session-token')
    expect(localStorage.getItem('lanory.token')).toBe('standalone-token')
    setSessionToken(null)
    expect(readToken()).toBe('standalone-token')
  })

  it('does not message a parent in standalone mode', () => {
    const post = vi.spyOn(window, 'postMessage')
    notifyParentRoute('/devices')
    expect(post).not.toHaveBeenCalled()
  })
})


describe('LuCI session handshake', () => {
  it('ignores other windows and origins before accepting the actual parent', async () => {
    history.replaceState(null, '', '/embed/topology')
    const parent = { postMessage: vi.fn() }
    vi.stubGlobal('parent', parent)
    vi.spyOn(document, 'referrer', 'get').mockReturnValue('http://localhost/cgi-bin/luci/')
    const ready = initializeEmbeddedSession()
    expect(parent.postMessage).toHaveBeenCalledWith({ type: 'lanory:ready' }, 'http://localhost')
    window.dispatchEvent(new MessageEvent('message', { source: parent as unknown as Window, origin: 'http://evil.example', data: { type: 'lanory:session', token: 'wrong' } }))
    window.dispatchEvent(new MessageEvent('message', { source: window, origin: 'http://localhost', data: { type: 'lanory:session', token: 'wrong' } }))
    expect(readToken()).toBeNull()
    window.dispatchEvent(new MessageEvent('message', { source: parent as unknown as Window, origin: 'http://localhost', data: { type: 'lanory:session', token: 'session-secret' } }))
    await ready
    expect(readToken()).toBe('session-secret')
    expect(localStorage.getItem('lanory.token')).toBeNull()
  })

  it('fails with a bounded wait when the parent session is unavailable', async () => {
    vi.useFakeTimers()
    history.replaceState(null, '', '/embed/')
    vi.stubGlobal('parent', { postMessage: vi.fn() })
    vi.spyOn(document, 'referrer', 'get').mockReturnValue('http://localhost/cgi-bin/luci/')
    const ready = expect(initializeEmbeddedSession()).rejects.toThrow('sign in again')
    await vi.advanceTimersByTimeAsync(12_000)
    await ready
    expect(readToken()).toBeNull()
  })
})

/**
 * Records a preference the way the Settings page does: through the store's own
 * actions, which is what persists it to `lanory.ui` (and, for the theme, to
 * `lanory.theme`).
 */
function choose(choice: { language?: 'zh-CN' | 'en'; theme?: 'light' | 'dark' | 'system' }) {
  if (choice.language) useUIStore.getState().setLang(choice.language)
  if (choice.theme) {
    useUIStore.getState().setTheme(choice.theme)
    setTheme(choice.theme)
  }
}

/** Boots an embedded session and hands the host context to the app. */
function startEmbedded() {
  history.replaceState(null, '', '/embed/topology')
  const parent = { postMessage: vi.fn() }
  vi.stubGlobal('parent', parent)
  vi.spyOn(document, 'referrer', 'get').mockReturnValue('http://localhost/cgi-bin/luci/')
  void initializeEmbeddedSession().catch(() => {})
  return parent
}

function sendHostContext(parent: { postMessage: unknown }, data: Record<string, unknown>) {
  window.dispatchEvent(new MessageEvent('message', { source: parent as unknown as Window, origin: 'http://localhost', data }))
}

describe('LuCI host context', () => {
  it('applies the host language and theme when the user has chosen nothing', () => {
    const parent = startEmbedded()
    expect(useUIStore.getState().language).toBe('en')
    sendHostContext(parent, { type: 'lanory:host-context', lang: 'zh-CN', theme: 'dark' })
    expect(useUIStore.getState().language).toBe('zh-CN')
    expect(useUIStore.getState().theme).toBe('dark')
    expect(getLanguage()).toBe('zh-CN')
    expect(getTheme()).toBe('dark')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    expect(document.documentElement.lang).toBe('zh-CN')
  })

  it('applies each field independently', () => {
    const parent = startEmbedded()
    sendHostContext(parent, { type: 'lanory:host-context', lang: 'zh-CN' })
    expect(useUIStore.getState().language).toBe('zh-CN')
    // No theme in the message, so the theme is left to the app's own default.
    expect(useUIStore.getState().theme).toBe('system')
  })

  it('ignores an unrecognised language or theme instead of applying it', () => {
    const parent = startEmbedded()
    sendHostContext(parent, { type: 'lanory:host-context', lang: 'fr', theme: 'solarized' })
    expect(useUIStore.getState().language).toBe('en')
    expect(useUIStore.getState().theme).toBe('system')
    expect(getLanguage()).toBe('en')
  })

  it('ignores a message from another window or another origin', () => {
    startEmbedded()
    const impostor = { postMessage: vi.fn() }
    window.dispatchEvent(new MessageEvent('message', { source: window, origin: 'http://localhost', data: { type: 'lanory:host-context', lang: 'zh-CN', theme: 'dark' } }))
    window.dispatchEvent(new MessageEvent('message', { source: impostor as unknown as Window, origin: 'http://evil.example', data: { type: 'lanory:host-context', lang: 'zh-CN', theme: 'dark' } }))
    expect(useUIStore.getState().language).toBe('en')
    expect(useUIStore.getState().theme).toBe('system')
  })

  it('does not overwrite an explicit user language', () => {
    choose({ language: 'zh-CN' })
    const parent = startEmbedded()
    sendHostContext(parent, { type: 'lanory:host-context', lang: 'en' })
    expect(useUIStore.getState().language).toBe('zh-CN')
    expect(getLanguage()).toBe('zh-CN')
  })

  it('does not overwrite an explicit user theme', () => {
    choose({ theme: 'light' })
    const parent = startEmbedded()
    sendHostContext(parent, { type: 'lanory:host-context', theme: 'dark' })
    expect(useUIStore.getState().theme).toBe('light')
    expect(getTheme()).toBe('light')
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })

  it('keeps following the host across visits while the user has chosen nothing', () => {
    // A previous visit adopted the host's values, so the store holds them and
    // records them as the host's - and that must not be mistaken for a choice.
    localStorage.setItem('lanory.host-context', JSON.stringify({ lang: 'zh-CN', theme: 'dark' }))
    choose({ language: 'zh-CN', theme: 'dark' })
    const parent = startEmbedded()
    sendHostContext(parent, { type: 'lanory:host-context', lang: 'en', theme: 'light' })
    expect(useUIStore.getState().language).toBe('en')
    expect(useUIStore.getState().theme).toBe('light')
    expect(getLanguage()).toBe('en')
    expect(getTheme()).toBe('light')
  })

  it('stops following the host once the user picks a value', () => {
    // The user disagreed with the host: 'dark' is theirs, not the host's 'light',
    // which this app took on a previous visit and therefore recorded.
    localStorage.setItem('lanory.host-context', JSON.stringify({ theme: 'light' }))
    choose({ theme: 'dark' })
    const parent = startEmbedded()
    sendHostContext(parent, { type: 'lanory:host-context', theme: 'light' })
    expect(useUIStore.getState().theme).toBe('dark')
    expect(getTheme()).toBe('dark')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
  })

  it('does not apply a host value in standalone mode', () => {
    history.replaceState(null, '', '/topology')
    const parent = { postMessage: vi.fn() }
    vi.stubGlobal('parent', parent)
    vi.spyOn(document, 'referrer', 'get').mockReturnValue('http://localhost/cgi-bin/luci/')
    sendHostContext(parent, { type: 'lanory:host-context', lang: 'zh-CN', theme: 'dark' })
    expect(useUIStore.getState().language).toBe('en')
    expect(useUIStore.getState().theme).toBe('system')
  })
})
