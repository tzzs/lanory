import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { render, waitFor } from '@testing-library/react'
import { writeToken } from '@/lib/api'
import { useRealtime, parseFrames, type RealtimeEvent } from './useRealtime'

/**
 * SSE client tests.
 *
 * The stream is read with fetch because the API requires a bearer token and
 * EventSource cannot send an Authorization header. Lanory also refuses a token
 * in the query string, so the EventSource path could never authenticate: these
 * tests lock the header form in.
 */

describe('parseFrames', () => {
  it('returns one frame per blank-line-separated block', () => {
    const frames = parseFrames('id: 1\nevent: a\ndata: {}\n\nid: 2\nevent: b\ndata: {}\n\n')
    expect(frames).toHaveLength(2)
    expect(frames[0]).toEqual({ id: '1', event: 'a', data: '{}' })
    expect(frames[1]).toEqual({ id: '2', event: 'b', data: '{}' })
  })

  it('drops comment lines', () => {
    const frames = parseFrames(': heartbeat\nid: 7\ndata: {"seq":7}\n\n')
    expect(frames).toHaveLength(1)
    expect(frames[0]).toEqual({ id: '7', data: '{"seq":7}' })
  })

  it('joins multi-line data', () => {
    const frames = parseFrames('data: {"seq":\ndata: 1}\n\n')
    expect(frames[0].data).toBe('{"seq":\n1}')
  })

  it('strips only the single space after the field colon', () => {
    // Per the SSE spec exactly one leading space is removed; a second one is
    // part of the value.
    const frames = parseFrames('data:  two spaces\n\n')
    expect(frames[0].data).toBe(' two spaces')
  })

  it('accepts a field with no colon as a value-less field', () => {
    const frames = parseFrames('data\n\n')
    expect(frames[0]).toEqual({ data: '' })
  })

  it('ignores an incomplete trailing block', () => {
    // The hook only ever hands over text up to a blank line, but a defensive
    // parser must not emit a frame whose terminator never arrived.
    const frames = parseFrames('id: 9\ndata: {"seq":9}')
    expect(frames).toHaveLength(0)
  })

  it('handles CRLF line endings', () => {
    const frames = parseFrames('id: 3\r\nevent: a\r\ndata: {}\r\n\r\n')
    expect(frames[0]).toEqual({ id: '3', event: 'a', data: '{}' })
  })
})

/** Renders the hook and captures the events it emits. */
function mountStream() {
  const events: RealtimeEvent[] = []
  function Probe() {
    useRealtime({ onEvent: (e) => events.push(e) })
    return null
  }
  render(<Probe />)
  return { events }
}

/** Builds a streaming Response whose body yields the given chunks. */
function streamResponse(chunks: string[]): Response {
  const encoder = new TextEncoder()
  let i = 0
  const body = new ReadableStream<Uint8Array>({
    pull(controller) {
      if (i < chunks.length) {
        controller.enqueue(encoder.encode(chunks[i++]))
      } else {
        controller.close()
      }
    },
  })
  return new Response(body, { status: 200, headers: { 'Content-Type': 'text/event-stream' } })
}

describe('useRealtime', () => {
  const fetchMock = vi.fn()

  beforeEach(() => {
    writeToken(null)
    fetchMock.mockReset()
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('sends the token as a header, never in the query string', async () => {
    writeToken('secret-token')
    fetchMock.mockResolvedValue(streamResponse([]))
    mountStream()

    await waitFor(() => expect(fetchMock).toHaveBeenCalled())
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    const headers = init.headers as Record<string, string>

    expect(headers.Authorization).toBe('Bearer secret-token')
    // A token in the URL would leak into logs and the Referer header, and the
    // server rejects it anyway.
    expect(String(url)).not.toContain('token')
    expect(String(url)).toContain('/api/v1/events/stream')
  })

  it('reassembles a frame that arrives split across chunks', async () => {
    // The frame is cut in the middle of its data line: a client that acted on
    // every chunk instead of every complete frame would emit a truncated or
    // duplicated event here.
    fetchMock.mockResolvedValue(
      streamResponse([
        'id: 7\nevent: device.online\ndata: {"type":"device.on',
        'line","seq":7,"timestamp":"2026-10-22T00:00:00Z"}\n\n',
      ]),
    )
    const { events } = mountStream()

    await waitFor(() => expect(events.length).toBe(1))
    expect(events[0].type).toBe('device.online')
    expect(events[0].seq).toBe(7)
  })

  it('consumes complete frames once across later events and heartbeats', async () => {
    fetchMock.mockResolvedValue(streamResponse([
      'id: 1\nevent: probe.completed\ndata: {"seq":1}\n\n',
      ': heartbeat\n\n',
      'id: 2\nevent: probe.completed\ndata: {"seq":2',
      '}\n\n',
      ': heartbeat\n\n',
    ]))
    const { events } = mountStream()
    await waitFor(() => expect(events.map(e => e.seq)).toEqual([1, 2]))
  })

  it('dispatches named events with their type', async () => {
    fetchMock.mockResolvedValue(
      streamResponse(['id: 1\nevent: link.updated\ndata: {"seq":1}\n\n']),
    )
    const { events } = mountStream()

    await waitFor(() => expect(events.length).toBe(1))
    expect(events[0].type).toBe('link.updated')
  })

  it('ignores an unknown event name', async () => {
    // An unexpected name is dropped rather than guessed at: the payload would
    // not be validated against any shape the app knows.
    fetchMock.mockResolvedValue(
      streamResponse(['id: 1\nevent: something.new\ndata: {"seq":1}\n\n']),
    )
    const { events } = mountStream()

    await new Promise((r) => setTimeout(r, 20))
    expect(events.length).toBe(0)
  })

  it('does not dispatch a frame that has no terminating blank line', async () => {
    fetchMock.mockResolvedValue(streamResponse(['id: 9\ndata: {"seq":9}\n']))
    const { events } = mountStream()

    await new Promise((r) => setTimeout(r, 20))
    expect(events.length).toBe(0)
  })

  it('retries after the stream ends', async () => {
    fetchMock.mockResolvedValueOnce(streamResponse(['id: 1\ndata: {"seq":1}\n\n']))
    fetchMock.mockResolvedValueOnce(streamResponse(['id: 2\ndata: {"seq":2}\n\n']))
    const { events } = mountStream()

    // The first attempt opens immediately; the retry waits out the initial
    // backoff, which is one second.
    await waitFor(() => expect(events.length).toBe(2), { timeout: 5_000 })
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('does not connect when disabled', async () => {
    function Disabled() {
      useRealtime({ enabled: false })
      return null
    }
    render(<Disabled />)
    await new Promise((r) => setTimeout(r, 20))
    expect(fetchMock).not.toHaveBeenCalled()
  })
})
