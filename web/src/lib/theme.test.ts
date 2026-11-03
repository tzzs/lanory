import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { getTheme, initTheme, setTheme } from '@/lib/theme'

function setSystemDark(matches: boolean) {
  const listeners = new Set<(event: MediaQueryListEvent) => void>()
  const media = {
    matches,
    addEventListener: (_type: string, listener: (event: MediaQueryListEvent) => void) => listeners.add(listener),
    removeEventListener: (_type: string, listener: (event: MediaQueryListEvent) => void) => listeners.delete(listener),
  }
  vi.stubGlobal('matchMedia', vi.fn(() => media))
  return { media, emit: () => listeners.forEach((listener) => listener({ matches: media.matches } as MediaQueryListEvent)) }
}

describe('theme helpers', () => {
  beforeEach(() => localStorage.clear())
  afterEach(() => vi.unstubAllGlobals())

  it('uses only supported persisted values and falls back to system', () => {
    expect(getTheme()).toBe('system')
    localStorage.setItem('lanory.theme', 'neon')
    expect(getTheme()).toBe('system')
    for (const mode of ['light', 'dark', 'system'] as const) {
      localStorage.setItem('lanory.theme', mode)
      expect(getTheme()).toBe(mode)
    }
  })

  it('applies dark and light modes to the root class and color scheme', () => {
    setTheme('dark')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    expect(document.documentElement.style.colorScheme).toBe('dark')
    setTheme('light')
    expect(document.documentElement.classList.contains('dark')).toBe(false)
    expect(document.documentElement.style.colorScheme).toBe('light')
  })

  it('follows system preference changes only while system mode is selected', () => {
    const system = setSystemDark(true)
    const cleanup = initTheme()
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    system.media.matches = false
    system.emit()
    expect(document.documentElement.classList.contains('dark')).toBe(false)
    setTheme('dark')
    system.media.matches = false
    system.emit()
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    cleanup()
  })
})
