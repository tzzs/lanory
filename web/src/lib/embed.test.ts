import { afterEach, describe, expect, it, vi } from 'vitest'
import { isEmbedded, trustedParentOrigin, notifyParentRoute, initializeEmbeddedSession } from './embed'
import { readToken, setSessionToken, writeToken } from './api'

afterEach(() => { setSessionToken(null); writeToken(null); vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.useRealTimers(); history.replaceState(null, '', '/') })

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
