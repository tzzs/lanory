import { readFileSync } from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'
import { DICTIONARIES, setLanguage, translateReason } from '@/i18n'

/**
 * The backend owns the reason codes; the frontend owns the wording. These
 * tests hold that contract in place.
 *
 * A reason code with no template renders the English fallback, which is
 * exactly the bug this file exists to prevent: a panel that is half Chinese
 * and half English because one code was never translated.
 */

/** The reason codes declared by the backend. */
function backendReasonCodes(): string[] {
  const source = readFileSync(
    path.resolve(import.meta.dirname, '../../../internal/model/reason.go'),
    'utf8',
  )
  const codes = new Set<string>()
  // Every code is declared as `ReasonSomething ReasonCode = "some.code"`.
  const pattern = /Reason\w+\s+ReasonCode\s*=\s*"([^"]+)"/g
  for (const match of source.matchAll(pattern)) {
    codes.add(match[1])
  }
  return [...codes]
}

describe('reason code templates', () => {
  const codes = backendReasonCodes()

  it('finds the backend reason codes', () => {
    // Guards the regex above: if it stops matching, the test below would
    // pass vacuously.
    expect(codes.length).toBeGreaterThan(20)
    expect(codes).toContain('probe.unreachable_consecutive')
  })

  it('has a template in both languages for every backend reason code', () => {
    const zh = DICTIONARIES['zh-CN']
    const en = DICTIONARIES.en
    const missing: string[] = []
    for (const code of codes) {
      if (!zh[`reason.${code}`]) missing.push(`zh-CN: reason.${code}`)
      if (!en[`reason.${code}`]) missing.push(`en: reason.${code}`)
    }
    expect(missing).toEqual([])
  })

  it('does not leave a template for a code the backend no longer has', () => {
    const templates = Object.keys(DICTIONARIES.en).filter((key) => key.startsWith('reason.'))
    const stale = templates.filter((key) => !codes.includes(key.slice('reason.'.length)))
    expect(stale).toEqual([])
  })
})

describe('dictionary parity', () => {
  const zh = DICTIONARIES['zh-CN']
  const en = DICTIONARIES.en

  it('defines the same keys in both languages', () => {
    const missingInEn = Object.keys(zh).filter((key) => !(key in en))
    const missingInZh = Object.keys(en).filter((key) => !(key in zh))
    expect(missingInEn).toEqual([])
    expect(missingInZh).toEqual([])
  })

  it('uses the same placeholders in both languages', () => {
    const placeholders = (value: string) => (value.match(/\{[^}]+\}/g) ?? []).sort()
    const mismatched: string[] = []
    for (const [key, value] of Object.entries(zh)) {
      const other = en[key]
      if (other === undefined) continue
      if (placeholders(value).join(',') !== placeholders(other).join(',')) {
        mismatched.push(key)
      }
    }
    expect(mismatched).toEqual([])
  })
})

describe('translateReason', () => {
  it('renders the template with the parameters', () => {
    setLanguage('zh-CN')
    expect(
      translateReason(
        { code: 'probe.unreachable_consecutive', params: { failures: '3', source: 'lanory', protocol: 'icmp' } },
        'fallback',
      ),
    ).toBe('从 lanory 通过 icmp 连续 3 次探测无响应，近期没有成功记录')
  })

  it('falls back to the English text for an unknown code', () => {
    setLanguage('zh-CN')
    expect(translateReason({ code: 'nope.nothing' }, 'the original text')).toBe('the original text')
  })

  it('falls back to the English text when there is no code at all', () => {
    setLanguage('zh-CN')
    expect(translateReason(undefined, 'the original text')).toBe('the original text')
  })

  it('localises an enum parameter but leaves free text alone', () => {
    setLanguage('zh-CN')
    // `admin_state` is an enum: the frontend owns the wording.
    expect(
      translateReason(
        { code: 'anomaly.no_carrier', params: { admin_state: 'up', interface: 'eth2' } },
        'fallback',
      ),
    ).toContain('已启用')
    // An interface name is free text and must survive untouched, even when it
    // happens to be the same word as a state token.
    expect(
      translateReason(
        { code: 'anomaly.no_carrier', params: { admin_state: 'down', interface: 'up' } },
        'fallback',
      ),
    ).toContain('up 没有物理载波')
  })
})
