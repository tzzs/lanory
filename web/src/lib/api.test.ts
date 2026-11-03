import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError, api, readToken, setSessionToken, writeToken } from '@/lib/api'

function response(status: number, body = '', statusText = 'response status') {
  return new Response(status === 204 ? null : body, { status, statusText })
}

describe('API client', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn())
    localStorage.clear()
    setSessionToken(null)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    setSessionToken(null)
  })

  it('reads, writes and clears the standalone token, while session credentials take precedence', () => {
    writeToken('stored-token')
    expect(readToken()).toBe('stored-token')
    setSessionToken('session-token')
    writeToken('replacement-token')
    expect(readToken()).toBe('session-token')
    setSessionToken(null)
    expect(readToken()).toBe('replacement-token')
    writeToken(null)
    expect(readToken()).toBeNull()
  })

  it('adds auth and CSRF headers to mutating requests and normalizes successful JSON', async () => {
    writeToken('secret')
    vi.mocked(fetch).mockResolvedValue(response(200, JSON.stringify({ id: 'node-1' })))

    await expect(api.createDevice({ name: 'Sensor' } as never)).resolves.toEqual({ id: 'node-1' })
    expect(fetch).toHaveBeenCalledWith('/api/v1/devices', expect.objectContaining({
      method: 'POST',
      headers: expect.objectContaining({
        Authorization: 'Bearer secret',
        'X-Requested-With': 'XMLHttpRequest',
        'Content-Type': 'application/json',
      }),
      body: JSON.stringify({ name: 'Sensor' }),
    }))
  })

  it('returns undefined for no-content responses and null for invalid JSON', async () => {
    vi.mocked(fetch).mockResolvedValueOnce(response(204)).mockResolvedValueOnce(response(200, 'not-json'))
    await expect(api.deleteDevice('node/1')).resolves.toBeUndefined()
    await expect(api.health()).resolves.toBeNull()
    expect(fetch).toHaveBeenNthCalledWith(1, '/api/v1/devices/node%2F1?mode=hide', expect.anything())
  })

  it('normalizes HTTP and network errors into ApiError values', async () => {
    vi.mocked(fetch)
      .mockResolvedValueOnce(response(422, JSON.stringify({ error: 'bad input', code: 'invalid', details: { name: 'required' } })))
      .mockRejectedValueOnce(new Error('offline'))

    await expect(api.health()).rejects.toMatchObject({
      name: 'ApiError', status: 422, code: 'invalid', message: 'bad input', details: { name: 'required' },
    })
    await expect(api.health()).rejects.toMatchObject({ name: 'ApiError', status: 0, code: 'network' })
    expect(new ApiError(503, { error: '' }).message).toBe('request failed with status 503')
  })

  it('drops empty query values and serializes arrays and booleans', async () => {
    vi.mocked(fetch).mockResolvedValue(response(200, '{}'))
    await api.devices({ query: '', type: 'router', state: undefined, limit: 20, online_first: false } as never)
    expect(fetch).toHaveBeenCalledWith('/api/v1/devices?type=router&limit=20&online_first=false', expect.anything())
  })

  it('encodes identifiers and sends all resource operations to their API routes', async () => {
    vi.mocked(fetch).mockImplementation(async () => response(200, '{}'))
    await Promise.all([
      api.overview(), api.device('a/b'), api.patchDevice('a/b', {} as never), api.mergeDevice('a/b', {} as never),
      api.topology(), api.topologyEvidence(), api.createEdge({} as never), api.patchEdge('a/b', {} as never),
      api.deleteEdge('e1'), api.saveLayout({} as never), api.autoLayout(), api.discoveryStatus(),
      api.discoveryCollectors(), api.startScan(), api.discoveryJob('j/1'), api.cancelDiscoveryJob('j/1'),
      api.monitoringStatus(), api.probes(), api.createProbe({} as never), api.patchProbe('p/1', {} as never),
      api.deleteProbe('p1'), api.runProbe('p1'), api.monitoringHistory({ probe_id: 'p1' }),
      api.events({ type: 'device.online' }), api.event('e/1'), api.settings(), api.patchSettings({} as never),
    ])
    const urls = vi.mocked(fetch).mock.calls.map(([url]) => String(url))
    expect(urls).toContain('/api/v1/devices/a%2Fb')
    expect(urls).toContain('/api/v1/discovery/jobs/j%2F1/cancel')
    expect(urls).toContain('/api/v1/monitoring/probes/p%2F1')
    expect(urls).toContain('/api/v1/topology/evidence')
    expect(urls).toContain('/api/v1/settings')
  })
})
