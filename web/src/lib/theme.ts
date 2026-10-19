/**
 * Theme handling: light / dark / follow-system, persisted in localStorage.
 * The initial class is applied by an inline script in index.html to avoid a
 * flash of the wrong theme.
 */

export type ThemeMode = 'light' | 'dark' | 'system'

const STORAGE_KEY = 'lanory.theme'

function systemPrefersDark(): boolean {
  return typeof window !== 'undefined' && window.matchMedia?.('(prefers-color-scheme: dark)').matches
}

function apply(mode: ThemeMode): void {
  const root = document.documentElement
  const dark = mode === 'dark' || (mode === 'system' && systemPrefersDark())
  root.classList.toggle('dark', dark)
  root.style.colorScheme = dark ? 'dark' : 'light'
}

export function getTheme(): ThemeMode {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (stored === 'light' || stored === 'dark' || stored === 'system') return stored
  } catch {
    /* ignore */
  }
  return 'system'
}

export function setTheme(mode: ThemeMode): void {
  try {
    localStorage.setItem(STORAGE_KEY, mode)
  } catch {
    /* ignore */
  }
  apply(mode)
}

/** Applies the persisted theme and follows system changes while in system mode. */
export function initTheme(): () => void {
  apply(getTheme())
  const media = window.matchMedia?.('(prefers-color-scheme: dark)')
  if (!media) return () => {}
  const onChange = () => {
    if (getTheme() === 'system') apply('system')
  }
  media.addEventListener('change', onChange)
  return () => media.removeEventListener('change', onChange)
}
