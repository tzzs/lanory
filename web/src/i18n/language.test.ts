import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { detectLanguage, getLanguage, setLanguage, t } from '@/i18n'

const STORAGE_KEY = 'lanory.language'

/**
 * The language has one owner: the persisted UI store. These tests pin the two
 * halves of that contract - detection (`localStorage` > `navigator.language` >
 * `en`) and application (`setLanguage` keeps the module, storage and
 * `<html lang>` in sync) - so the three sources that used to compete
 * (`useUIStore`'s hardcoded default, this module's detection, and the hardcoded
 * `<html lang="zh-CN">`) cannot drift apart again.
 *
 * The store side - including the boot rehydration that pushes a persisted value
 * into this module - is covered by `src/stores/ui.test.ts`.
 */
describe('detectLanguage', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    localStorage.clear()
  })

  it('prefers a stored valid value over navigator.language', () => {
    vi.stubGlobal('navigator', { language: 'en-US' })
    localStorage.setItem(STORAGE_KEY, 'zh-CN')
    expect(detectLanguage()).toBe('zh-CN')
  })

  it('falls back to navigator.language when nothing is stored', () => {
    vi.stubGlobal('navigator', { language: 'zh-Hans-CN' })
    expect(detectLanguage()).toBe('zh-CN')
  })

  it('returns en when neither is available', () => {
    // A non-Chinese browser and no stored choice: English is the documented
    // fallback and also the dictionary fallback.
    vi.stubGlobal('navigator', { language: 'de-DE' })
    expect(detectLanguage()).toBe('en')
  })

  it('ignores an unrecognised stored value', () => {
    vi.stubGlobal('navigator', { language: 'de-DE' })
    localStorage.setItem(STORAGE_KEY, 'klingon')
    expect(detectLanguage()).toBe('en')
  })
})

describe('setLanguage', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  afterEach(() => {
    localStorage.clear()
  })

  it('sets document lang, the module value and localStorage together', () => {
    // `lib/devices.ts:getLocale()` reads document.documentElement.lang to pick
    // the locale for Intl formatting, so this attribute is behaviour.
    setLanguage('zh-CN')
    expect(document.documentElement.lang).toBe('zh-CN')
    expect(getLanguage()).toBe('zh-CN')
    expect(localStorage.getItem(STORAGE_KEY)).toBe('zh-CN')

    setLanguage('en')
    expect(document.documentElement.lang).toBe('en')
    expect(getLanguage()).toBe('en')
    expect(localStorage.getItem(STORAGE_KEY)).toBe('en')
  })

  it('switches the dictionary that t() reads', () => {
    setLanguage('zh-CN')
    const chinese = t('nav.settings')
    setLanguage('en')
    expect(chinese).not.toBe(t('nav.settings'))
    expect(t('nav.settings')).toBe('Settings')
  })
})
