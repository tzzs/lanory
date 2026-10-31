import { describe, expect, it } from 'vitest'
import { setLanguage } from '@/i18n'
import { eventTransition, eventValueLabel } from '@/lib/events'

/**
 * An event's old and new values are the raw tokens the backend stores. The
 * wording belongs to the frontend, so these tests pin the mapping: a value
 * that is not a known token must survive untouched.
 */
describe('eventValueLabel', () => {
  it('renders a device state in the current language', () => {
    setLanguage('zh-CN')
    expect(eventValueLabel('device.offline', 'offline')).toBe('离线')
    expect(eventValueLabel('device.online', 'online')).toBe('在线')
  })

  it('renders a carrier state in the current language', () => {
    setLanguage('zh-CN')
    expect(eventValueLabel('link.down', 'down')).toBe('已断开')
    expect(eventValueLabel('link.up', 'up')).toBe('已连接')
  })

  it('keeps the unit of a speed change', () => {
    setLanguage('zh-CN')
    expect(eventValueLabel('speed.changed', '1000')).toBe('1000 Mbps')
  })

  it('leaves a value that is not a token alone', () => {
    setLanguage('zh-CN')
    expect(eventValueLabel('device.ip_changed', '192.168.100.150')).toBe('192.168.100.150')
    expect(eventValueLabel('device.discovered', 'up')).toBe('up')
  })

  it('does not translate free values on a generic device update', () => {
    setLanguage('zh-CN')
    expect(eventValueLabel('device.updated', 'up')).toBe('up')
    expect(eventTransition('device.updated', 'online', 'offline')).toBe('online → offline')
  })
})

describe('eventTransition', () => {
  it('renders both ends of a transition', () => {
    setLanguage('zh-CN')
    expect(eventTransition('link.down', 'up', 'down')).toBe('已连接 → 已断开')
  })

  it('is empty when the event has no transition', () => {
    setLanguage('zh-CN')
    expect(eventTransition('device.discovered', '', '')).toBe('')
    expect(eventTransition('device.discovered', 'up', '')).toBe('')
  })
})
