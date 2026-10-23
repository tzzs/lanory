/**
 * Server-Sent Events subscription.
 *
 * The backend emits JSON frames on /api/v1/events/stream. This hook reconnects
 * with exponential backoff and uses the Last-Event-ID header so a reconnect
 * after a blip does not silently miss events.
 *
 * The stream is read with fetch rather than EventSource because the API is
 * protected by a bearer token and EventSource cannot set an Authorization
 * header. A token in the query string is the usual workaround, but Lanory
 * deliberately refuses those (URLs end up in logs and in the Referer header), so
 * the query form answers 401 and the stream never opens. fetch streams the
 * response body as it arrives, which gives the same live behaviour with a
 * header that actually authenticates.
 */

import { useEffect, useRef, useState, useCallback } from 'react'
import { readToken } from '@/lib/api'

export interface RealtimeEvent<T = unknown> {
  type: string
  seq: number
  timestamp: string
  payload?: T
}

export interface UseRealtimeOptions {
  enabled?: boolean
  onEvent?: (event: RealtimeEvent) => void
  /** Called when the connection state changes. */
  onConnectionChange?: (connected: boolean) => void
}

const MAX_BACKOFF_MS = 30_000

// The event names the backend publishes. A frame is dispatched by name, so the
// listener is registered per name; anything unexpected is ignored rather than
// guessed at.
const EVENT_NAMES = [
  'device.updated',
  'device.online',
  'device.offline',
  'topology.updated',
  'link.updated',
  'probe.completed',
  'discovery.started',
  'discovery.completed',
  'settings.updated',
]

/**
 * Splits an SSE byte stream into frames.
 *
 * A frame ends at a blank line. Each frame is returned as the map of its
 * field names to values, which is all this client needs: it reads `id`, `event`
 * and `data`. Comment lines (": heartbeat") are dropped, and a `data:` value may
 * span several lines, which are joined with "\n" per the SSE spec.
 *
 * A block without its terminating blank line is incomplete and is dropped, not
 * returned: dispatching a half-received frame would emit a truncated event, and
 * the next chunk would then emit the rest of it as a second one. The rule lives
 * here rather than at the call site so the guarantee does not depend on every
 * caller slicing the stream correctly first.
 *
 * Exported for the tests: this is the piece that decides when a chunk of bytes
 * becomes a complete event.
 */
export function parseFrames(chunk: string): Array<Record<string, string>> {
  const frames: Array<Record<string, string>> = []
  // Only text up to the last frame separator is complete. Anything after it is
  // a partial frame still in flight. The separator is a blank line in either
  // convention: "\n\n" or "\r\n\r\n".
  const separator = /\r?\n\r?\n/
  let lastEnd = -1
  let match: RegExpExecArray | null
  const scan = new RegExp(separator.source, 'g')
  while ((match = scan.exec(chunk)) !== null) {
    lastEnd = match.index + match[0].length
  }
  if (lastEnd === -1) return frames
  for (const block of chunk.slice(0, lastEnd).split(separator)) {
    if (block.trim() === '') continue
    const fields: Record<string, string> = {}
    const dataLines: string[] = []
    for (const line of block.split(/\r?\n/)) {
      if (line === '' || line.startsWith(':')) continue
      const colon = line.indexOf(':')
      const name = colon === -1 ? line : line.slice(0, colon)
      let value = colon === -1 ? '' : line.slice(colon + 1)
      if (value.startsWith(' ')) value = value.slice(1)
      if (name === 'data') {
        dataLines.push(value)
      } else {
        fields[name] = value
      }
    }
    if (dataLines.length > 0) fields.data = dataLines.join('\n')
    frames.push(fields)
  }
  return frames
}

export function useRealtime({ enabled = true, onEvent, onConnectionChange }: UseRealtimeOptions = {}) {
  const [connected, setConnected] = useState(false)
  const [lastSeq, setLastSeq] = useState(0)

  // Keep callbacks in refs so a changing callback identity never re-opens the
  // connection (which would make the SSE stream flap).
  const onEventRef = useRef(onEvent)
  onEventRef.current = onEvent
  const onConnectionChangeRef = useRef(onConnectionChange)
  onConnectionChangeRef.current = onConnectionChange

  const lastSeqRef = useRef(0)
  const setSeq = useCallback((seq: number) => {
    lastSeqRef.current = seq
    setLastSeq(seq)
  }, [])

  useEffect(() => {
    if (!enabled) return

    let controller: AbortController | null = null
    let retryTimer: number | undefined
    let backoff = 1_000
    let disposed = false

    const connect = async () => {
      if (disposed) return
      // The token is read on every attempt: it is stored by the token prompt
      // and may change between reconnects.
      const token = readToken()
      controller = new AbortController()

      const headers: Record<string, string> = { Accept: 'text/event-stream' }
      if (token) headers.Authorization = `Bearer ${token}`
      // Replay anything the bus retained since the last frame seen, so a
      // reconnect after a blip does not silently miss events.
      if (lastSeqRef.current > 0) headers['Last-Event-ID'] = String(lastSeqRef.current)

      let res: Response
      try {
        res = await fetch('/api/v1/events/stream', {
          headers,
          cache: 'no-store',
          signal: controller.signal,
        })
      } catch {
        // Aborted on unmount, or the network is down: both are handled by the
        // cleanup path or the retry below.
        if (disposed) return
        scheduleRetry()
        return
      }

      if (!res.ok || res.body === null) {
        // 401 means the token was rejected; 5xx means the server is unhappy.
        // Either way the stream is not open, so retry with backoff.
        scheduleRetry()
        return
      }

      backoff = 1_000
      setConnected(true)
      onConnectionChangeRef.current?.(true)

      const reader = res.body.getReader()
      const decoder = new TextDecoder()
      let buffer = ''
      try {
        for (;;) {
          const { done, value } = await reader.read()
          if (done) break
          buffer += decoder.decode(value, { stream: true })
          // parseFrames only returns complete frames, so whatever trails the
          // last blank line stays buffered until its terminator arrives.
          let consumed = 0
          for (const match of buffer.matchAll(/\r?\n\r?\n/g)) consumed = match.index! + match[0].length
          const complete = buffer.slice(0, consumed)
          buffer = buffer.slice(consumed)
          for (const frame of parseFrames(complete)) {
            const seq = Number(frame.id)
            if (Number.isFinite(seq) && seq > 0) setSeq(seq)
            if (!frame.data) continue
            const name = frame.event || 'message'
            if (name !== 'message' && !EVENT_NAMES.includes(name)) continue
            try {
              const event = JSON.parse(frame.data) as RealtimeEvent
              if (typeof event.seq === 'number') setSeq(event.seq)
              onEventRef.current?.(name === 'message' ? event : { ...event, type: name })
            } catch {
              /* ignore malformed frames */
            }
          }
        }
      } catch {
        // The connection dropped mid-stream; fall through to the retry.
      } finally {
        reader.cancel().catch(() => {})
      }

      if (disposed) return
      setConnected(false)
      onConnectionChangeRef.current?.(false)
      scheduleRetry()
    }

    function scheduleRetry() {
      if (disposed) return
      setConnected(false)
      onConnectionChangeRef.current?.(false)
      if (retryTimer) window.clearTimeout(retryTimer)
      retryTimer = window.setTimeout(() => void connect(), backoff)
      backoff = Math.min(backoff * 2, MAX_BACKOFF_MS)
    }

    void connect()

    return () => {
      disposed = true
      if (retryTimer) window.clearTimeout(retryTimer)
      controller?.abort()
      setConnected(false)
      onConnectionChangeRef.current?.(false)
    }
  }, [enabled, setSeq])

  return { connected, lastSeq }
}

/**
 * Subscribes to realtime events and invalidates the relevant queries.
 * Mounted once near the root of the app.
 */
export function useRealtimeInvalidation(invalidate: (event: RealtimeEvent) => void) {
  const invalidateRef = useRef(invalidate)
  invalidateRef.current = invalidate

  return useRealtime({
    onEvent: (event) => invalidateRef.current(event),
  })
}
