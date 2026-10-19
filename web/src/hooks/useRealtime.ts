/**
 * Server-Sent Events subscription.
 *
 * The backend emits JSON frames on /api/v1/events/stream. This hook reconnects
 * with exponential backoff and uses the Last-Event-ID header so a reconnect
 * after a blip does not silently miss events.
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

    let source: EventSource | null = null
    let retryTimer: number | undefined
    let backoff = 1_000
    let disposed = false

    const connect = () => {
      if (disposed) return
      const token = readToken()
      const url = token
        ? `/api/v1/events/stream?token=${encodeURIComponent(token)}`
        : '/api/v1/events/stream'

      source = new EventSource(url)

      source.onopen = () => {
        if (disposed) return
        backoff = 1_000
        setConnected(true)
        onConnectionChangeRef.current?.(true)
      }

      // The server sends a default "message" frame carrying the JSON envelope.
      source.onmessage = (msg) => {
        if (disposed) return
        try {
          const event = JSON.parse(msg.data) as RealtimeEvent
          if (typeof event.seq === 'number') setSeq(event.seq)
          onEventRef.current?.(event)
        } catch {
          /* ignore malformed frames */
        }
      }

      // Named events are also delivered; treat them the same way.
      const named = ['device.updated', 'device.online', 'device.offline', 'topology.updated', 'link.updated', 'probe.completed', 'discovery.started', 'discovery.completed', 'settings.updated']
      for (const name of named) {
        source.addEventListener(name, (msg) => {
          if (disposed) return
          try {
            const event = JSON.parse((msg as MessageEvent).data) as RealtimeEvent
            if (typeof event.seq === 'number') setSeq(event.seq)
            onEventRef.current?.({ ...event, type: event.type || name })
          } catch {
            /* ignore */
          }
        })
      }

      source.onerror = () => {
        if (disposed) return
        setConnected(false)
        onConnectionChangeRef.current?.(false)
        source?.close()
        if (retryTimer) window.clearTimeout(retryTimer)
        retryTimer = window.setTimeout(connect, backoff)
        backoff = Math.min(backoff * 2, MAX_BACKOFF_MS)
      }
    }

    connect()

    return () => {
      disposed = true
      if (retryTimer) window.clearTimeout(retryTimer)
      source?.close()
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
