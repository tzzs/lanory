import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useUIStore } from '@/stores/ui'
import { getLanguage } from '@/i18n'

const UI_KEY = 'lanory.ui'
const LANGUAGE_KEY = 'lanory.language'

/**
 * The persisted UI store is the single source of truth for the language: it
 * seeds itself from `detectLanguage()`, writes every change through
 * `setLanguage()` (which owns `<html lang>`), and pushes a rehydrated value into
 * the i18n module on boot. These tests cover the store half of that contract;
 * detection and `setLanguage` itself live in `src/i18n/language.test.ts`.
 */
describe('useUIStore language', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    localStorage.clear()
  })

  it('setLang updates the store, localStorage and document lang', () => {
    useUIStore.getState().setLang('zh-CN')

    expect(useUIStore.getState().language).toBe('zh-CN')
    expect(getLanguage()).toBe('zh-CN')
    // getLocale() in lib/devices.ts formats dates from this attribute.
    expect(document.documentElement.lang).toBe('zh-CN')
    expect(localStorage.getItem(LANGUAGE_KEY)).toBe('zh-CN')

    const persisted = JSON.parse(localStorage.getItem(UI_KEY) ?? '{}') as { state?: { language?: string } }
    expect(persisted.state?.language).toBe('zh-CN')

    useUIStore.getState().setLang('en')
    expect(useUIStore.getState().language).toBe('en')
    expect(document.documentElement.lang).toBe('en')
    expect(localStorage.getItem(LANGUAGE_KEY)).toBe('en')
  })

  it('starts from the detected language instead of a hardcoded default', async () => {
    // A Chinese browser with nothing stored used to show English as the active
    // language in Settings while the dictionary rendered Chinese.
    vi.stubGlobal('navigator', { language: 'zh-CN' })
    vi.resetModules()

    const i18n = await import('@/i18n')
    const { useUIStore: freshStore } = await import('@/stores/ui')

    expect(i18n.detectLanguage()).toBe('zh-CN')
    expect(freshStore.getState().language).toBe('zh-CN')
    expect(i18n.getLanguage()).toBe('zh-CN')
  })

  it('rehydrates a persisted language into the i18n module on boot', async () => {
    // A returning user who chose Chinese: only the zustand payload is on disk,
    // and the browser is English. Detection alone would render the wrong
    // language until the user clicked something, which is the bug this covers.
    vi.stubGlobal('navigator', { language: 'en-US' })
    localStorage.setItem(UI_KEY, JSON.stringify({ state: { language: 'zh-CN' }, version: 0 }))
    vi.resetModules()

    const i18n = await import('@/i18n')
    expect(i18n.getLanguage()).toBe('en')

    const { useUIStore: freshStore } = await import('@/stores/ui')

    // Hydration must be complete by the time the module finishes evaluating:
    // main.tsx renders only after that, so this is what guarantees the first
    // paint already has the right dictionary and <html lang>.
    expect(freshStore.persist.hasHydrated()).toBe(true)
    expect(i18n.getLanguage()).toBe('zh-CN')
    expect(document.documentElement.lang).toBe('zh-CN')
  })
})
