import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { TokenPrompt, useTokenGate } from './TokenPrompt'
import { writeToken, readToken } from '@/lib/api'

/** Drives useTokenGate outside React so the probe can be observed directly. */
function mountTokenGate() {
  const seen: { required: boolean; checked: boolean }[] = []
  function Probe() {
    const { required, checked } = useTokenGate()
    seen.push({ required, checked })
    return null
  }
  render(<Probe />)
  return seen
}

describe('TokenPrompt', () => {
  it('renders the prompt with instructions', () => {
    writeToken(null)
    render(<TokenPrompt />)
    // The token label is present in both languages; the shell command is the
    // part that actually tells the user what to do.
    expect(screen.getByText(/cat \/etc\/lanory\/token/)).toBeTruthy()
  })

  it('stores the token and notifies on submit', () => {
    writeToken(null)
    const onDone = vi.fn()
    render(<TokenPrompt onAuthenticated={onDone} />)
    const input = document.querySelector('input[type="password"]') as HTMLInputElement
    fireEvent.change(input, { target: { value: 'abc123' } })
    fireEvent.click(screen.getByText(/Continue|进入/))
    expect(readToken()).toBe('abc123')
    expect(onDone).toHaveBeenCalled()
  })

  it('rejects an empty token', () => {
    writeToken(null)
    render(<TokenPrompt />)
    fireEvent.click(screen.getByText(/Continue|进入/))
    expect(readToken()).toBeNull()
  })
})

// The gate is what decides whether the prompt is shown at all, so its probe is
// locked down separately from the component's rendering.
describe('useTokenGate', () => {
  const fetchMock = vi.fn()

  beforeEach(() => {
    writeToken(null)
    fetchMock.mockReset()
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('requires a token when a protected route answers 401', async () => {
    fetchMock.mockResolvedValue({ ok: false, status: 401 })
    const seen = mountTokenGate()

    await waitFor(() => expect(seen.at(-1)?.checked).toBe(true))
    expect(seen.at(-1)?.required).toBe(true)
  })

  it('does not require a token when the API answers 200', async () => {
    fetchMock.mockResolvedValue({ ok: true, status: 200 })
    const seen = mountTokenGate()

    await waitFor(() => expect(seen.at(-1)?.checked).toBe(true))
    expect(seen.at(-1)?.required).toBe(false)
  })

  it('probes a protected route, not the public health endpoint', async () => {
    // /api/v1/health is public by design, so it answers 200 whether or not
    // authentication is on. Probing it is what left the router showing a bare
    // "Something went wrong" instead of the token prompt: the gate could never
    // fire, every page rendered, and each of them failed with a 401.
    fetchMock.mockResolvedValue({ ok: true, status: 200 })
    mountTokenGate()

    await waitFor(() => expect(fetchMock).toHaveBeenCalled())
    const [url] = fetchMock.mock.calls[0]
    expect(String(url)).not.toContain('/health')
    expect(String(url)).toContain('/api/v1/')
  })

  it('sends the stored token so a regenerated one brings the prompt back', async () => {
    writeToken('stale-token')
    fetchMock.mockResolvedValue({ ok: false, status: 401 })
    mountTokenGate()

    await waitFor(() => expect(fetchMock).toHaveBeenCalled())
    const init = fetchMock.mock.calls[0][1] as RequestInit
    const headers = init.headers as Record<string, string>
    expect(headers.Authorization).toBe('Bearer stale-token')
  })

  it('does not claim a token is needed when the API is unreachable', async () => {
    fetchMock.mockRejectedValue(new Error('network down'))
    const seen = mountTokenGate()

    await waitFor(() => expect(seen.at(-1)?.checked).toBe(true))
    expect(seen.at(-1)?.required).toBe(false)
  })
})
