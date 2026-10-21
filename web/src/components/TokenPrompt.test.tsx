import { describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { TokenPrompt } from './TokenPrompt'
import { writeToken, readToken } from '@/lib/api'

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
