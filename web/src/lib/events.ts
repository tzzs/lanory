import { stateTokenLabel } from '@/i18n'
import type { EventType } from '@/types/api'

/**
 * Renders the old or new value of an event in the user's language.
 *
 * The backend stores the raw token ("online", "up") because that is what the
 * state machine and the kernel report; the wording belongs to the frontend.
 *
 * An event's values are only meaningful together with its type: a device
 * event carries device states, a link event carries carrier states, and a
 * speed event carries a number of megabits per second. A value that is none
 * of those - an IP address, a MAC - is shown as it is.
 */
export function eventValueLabel(eventType: EventType | string, value: string): string {
  if (!value) return value
  switch (eventType) {
    case 'device.online':
    case 'device.offline':
    case 'device.updated':
      return stateTokenLabel(value)
    case 'link.up':
    case 'link.down':
      return stateTokenLabel(value)
    case 'speed.changed':
      return `${value} Mbps`
    default:
      return value
  }
}

/**
 * Renders the "old -> new" transition of an event, or an empty string when
 * the event has no transition to show.
 */
export function eventTransition(eventType: EventType | string, oldValue?: string, newValue?: string): string {
  if (!oldValue || !newValue) return ''
  return `${eventValueLabel(eventType, oldValue)} → ${eventValueLabel(eventType, newValue)}`
}
