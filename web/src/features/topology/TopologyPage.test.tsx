import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { TopologyPage } from './TopologyPage'
import { useUIStore } from '@/stores/ui'
import { t } from '@/i18n'
import type { Topology } from '@/types/api'

/**
 * Topology page tests.
 *
 * Two audit defects are covered here. Both are properties of the DOM/class
 * recipe rather than of computed layout, because jsdom applies no CSS:
 *
 *  1. React Flow's controls and minimap stay light in the dark theme. React
 *     Flow v12 themes them from the `colorMode` prop, so the page has to pass
 *     the *resolved* app theme (the store holds light/dark/system and the app
 *     puts `dark` on <html>); React Flow's own 'system' watches a different
 *     class and would disagree. The panels' icon colour has to come from a
 *     token too, or the icons are pale on a dark surface.
 *  2. The canvas overlays overlap at 390px. They used to be four absolutely
 *     positioned clusters; the toolbar, the hidden-offline badge and the save
 *     error are now one flex column above the canvas, and the hierarchy hint is
 *     dropped below `sm`.
 */

const now = new Date().toISOString()

function device(id: string, name: string, state: 'online' | 'offline') {
  return {
    id,
    name,
    hostname: '',
    type: 'router' as const,
    manufacturer: '',
    model: '',
    description: '',
    is_manual: false,
    is_locked: false,
    is_hidden: false,
    state,
    last_state_change: now,
    discovery_sources: ['openwrt' as const],
    is_gateway: false,
    is_self: false,
    first_seen: now,
    last_seen: now,
    created_at: now,
    updated_at: now,
  }
}

const topology: Topology = {
  nodes: [
    { id: 'router-1', kind: 'device', device: device('router-1', 'Router', 'online'), state: 'online' },
    { id: 'ap-1', kind: 'device', device: device('ap-1', 'Access Point', 'online'), state: 'online' },
    { id: 'tv-1', kind: 'device', device: device('tv-1', 'TV', 'offline'), state: 'offline' },
  ],
  edges: [
    {
      id: 'edge-1',
      source_id: 'router-1',
      target_id: 'ap-1',
      edge_type: 'physical_link',
      discovery_source: 'openwrt',
      confidence: 1,
      user_confirmed: true,
      locked: false,
      status: 'link_up',
      evidence_count: 1,
      unverified: false,
      last_observed: now,
      created_at: now,
      updated_at: now,
    },
  ],
  layout: [],
  generated_at: now,
}

const useTopology = vi.fn()
const useSaveLayout = vi.fn()
const useCreateEdge = vi.fn()
const useMonitoring = vi.fn()

vi.mock('@/hooks/useApi', () => ({
  useTopology: (...args: unknown[]) => useTopology(...args),
  useSaveLayout: (...args: unknown[]) => useSaveLayout(...args),
  useCreateEdge: (...args: unknown[]) => useCreateEdge(...args),
  useMonitoring: (...args: unknown[]) => useMonitoring(...args),
}))

/**
 * jsdom has no layout engine, so React Flow's own measurement-driven hooks
 * (`useReactFlow`, `useNodesInitialized`) and its panels (which read the
 * store) cannot run. The real provider stays — it owns the store the page's
 * hooks subscribe to — but the panels are replaced by plain elements carrying
 * the class list and style the page gives them, which is what is under test.
 */
vi.mock('@xyflow/react', async () => {
  const actual = await vi.importActual<typeof import('@xyflow/react')>('@xyflow/react')
  return {
    ...actual,
    ReactFlow: ({ children, colorMode }: { children?: React.ReactNode; colorMode?: string }) => (
      <div data-testid="react-flow" data-color-mode={colorMode}>
        {children}
      </div>
    ),
    Background: () => <div data-testid="rf__background" />,
    Controls: ({ className, style }: { className?: string; style?: React.CSSProperties }) => (
      <div data-testid="rf__controls" className={className} style={style} />
    ),
    MiniMap: ({ className, style }: { className?: string; style?: React.CSSProperties }) => (
      <div data-testid="rf__minimap" className={className} style={style} />
    ),
    useReactFlow: () => ({ fitView: () => Promise.resolve() }),
    useNodesInitialized: () => true,
  }
})

/** The class a React Flow panel renders is the only observable the DOM offers. */
function panelClass(testId: string): string[] {
  const panel = document.querySelector(`[data-testid="${testId}"]`)
  if (!panel) throw new Error(`no ${testId} was rendered`)
  return (panel as HTMLElement).className.split(/\s+/)
}

function panelStyleVar(testId: string, name: string): string | undefined {
  const panel = document.querySelector(`[data-testid="${testId}"]`) as HTMLElement | null
  return panel?.style.getPropertyValue(name)
}

function setSystemDark(dark: boolean) {
  window.matchMedia = ((query: string) => ({
    matches: query.includes('dark') ? dark : false,
    media: query,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia
}

function renderPage() {
  return render(
    <MemoryRouter>
      <TopologyPage />
    </MemoryRouter>,
  )
}

describe('TopologyPage', () => {
  beforeEach(() => {
    useTopology.mockReset()
    useTopology.mockReturnValue({ data: topology, isLoading: false, isError: false, error: null, refetch: vi.fn() })
    useSaveLayout.mockReset()
    useSaveLayout.mockReturnValue({ mutate: vi.fn(), isPending: false, isError: false, error: null })
    useCreateEdge.mockReset()
    useCreateEdge.mockReturnValue({ mutate: vi.fn(), isPending: false })
    useMonitoring.mockReset()
    useMonitoring.mockReturnValue({ data: { interfaces: [] } })
    useUIStore.setState({ theme: 'light' })
    setSystemDark(false)
    document.documentElement.classList.remove('dark')
  })

  afterEach(() => {
    document.documentElement.classList.remove('dark')
  })

  /* ------------------------------------------------------------------ */
  /* 11.1 dark theme controls                                          */
  /* ------------------------------------------------------------------ */

  it('passes the resolved dark mode to React Flow when the theme is dark', () => {
    useUIStore.setState({ theme: 'dark' })
    renderPage()
    expect(screen.getByTestId('react-flow').getAttribute('data-color-mode')).toBe('dark')
  })

  it('passes the resolved light mode to React Flow when the theme is light', () => {
    useUIStore.setState({ theme: 'light' })
    renderPage()
    expect(screen.getByTestId('react-flow').getAttribute('data-color-mode')).toBe('light')
  })

  it("resolves 'system' against the OS preference, not React Flow's own watcher", () => {
    useUIStore.setState({ theme: 'system' })
    setSystemDark(true)
    renderPage()
    expect(screen.getByTestId('react-flow').getAttribute('data-color-mode')).toBe('dark')

    // ...and the other way round, so 'system' in a light browser stays light.
    document.body.innerHTML = ''
    setSystemDark(false)
    renderPage()
    expect(screen.getByTestId('react-flow').getAttribute('data-color-mode')).toBe('light')
  })

  it('themes the controls and minimap with the app tokens in both modes', () => {
    useUIStore.setState({ theme: 'dark' })
    renderPage()

    // Surface and border come from the design tokens, so the panel is the
    // theme's surface rather than React Flow's light default.
    expect(panelClass('rf__controls')).toContain('!bg-surface')
    expect(panelClass('rf__controls')).toContain('!border-border')
    expect(panelClass('rf__minimap')).toContain('!border-border')
    expect(panelStyleVar('rf__minimap', '--xy-minimap-background-color')).toBe('var(--surface)')

    // The icon colour comes from `currentColor`, so the controls button colour
    // is the theme's foreground: pale icons on a dark panel were the defect.
    expect(panelStyleVar('rf__controls', '--xy-controls-button-color')).toBe('var(--fg)')
  })

  /* ------------------------------------------------------------------ */
  /* 11.3 overlays cooperate at 390px                                   */
  /* ------------------------------------------------------------------ */

  it('keeps the hierarchy hint below sm only', () => {
    renderPage()
    const hint = screen.getByText(t('topology.hierarchy_hint'))
    const classes = hint.className.split(/\s+/)
    expect(classes).toContain('hidden')
    expect(classes).toContain('sm:block')
  })

  it('stacks the toolbar, badge and error banner in one column instead of pinning them', () => {
    useSaveLayout.mockReturnValue({ mutate: vi.fn(), isPending: false, isError: true, error: new Error('save failed') })
    useUIStore.setState({ theme: 'light' })
    renderPage()

    // One overlay root, absolutely positioned, and the toolbar inside it is a
    // normal flex child — so the banner sits under however many rows the
    // toolbar wrapped into instead of at a fixed `top-16`.
    const overlay = document.querySelector('[data-testid="topology-overlays"]') as HTMLElement | null
    expect(overlay, 'the overlays should be wrapped in one column').not.toBeNull()
    expect(overlay?.className.split(/\s+/)).toContain('absolute')
    expect(overlay?.className.split(/\s+/)).toContain('flex-col')

    const banner = screen.getByRole('alert')
    const toolbar = screen.getByRole('button', { name: /reset layout|reset_layout/i })
    expect(overlay?.contains(banner)).toBe(true)
    expect(overlay?.contains(toolbar)).toBe(true)
    // The banner is not the toolbar's sibling at a fixed offset: it comes after
    // the toolbar in the column, so the toolbar's height pushes it down.
    const children = Array.from(overlay?.children ?? [])
    expect(children.indexOf(banner)).toBeGreaterThan(children.indexOf(toolbar.parentElement as HTMLElement))
    // Nothing is absolutely positioned inside the column any more.
    expect(banner.className).not.toContain('absolute')
  })

  it('bounds the toolbar to the viewport so it wraps instead of overflowing', () => {
    renderPage()
    const toolbar = screen.getByRole('button', { name: /reset layout|reset_layout/i }).parentElement as HTMLElement
    const classes = toolbar.className.split(/\s+/)
    expect(classes).toContain('flex-wrap')
    expect(classes.some((c) => c.startsWith('max-w-'))).toBe(true)
  })

  /* ------------------------------------------------------------------ */
  /* offline devices are hidden by default                              */
  /* ------------------------------------------------------------------ */

  it('hides offline devices by default and says how many', () => {
    renderPage()

    // The fixture has one offline device (the TV), so the badge reports it...
    expect(screen.getByText(t('topology.offline_hidden', { count: 1 }))).toBeTruthy()
    // ...and the toggle offers the way back rather than only reporting.
    const toggle = screen.getByRole('button', { name: new RegExp(t('topology.show_offline')) })
    expect(toggle.getAttribute('aria-pressed')).toBe('false')
  })

  it('reveals offline devices when the user asks for them', async () => {
    const { act } = await import('react')
    renderPage()

    const toggle = screen.getByRole('button', { name: new RegExp(t('topology.show_offline')) })
    act(() => {
      toggle.click()
    })

    // Pledging to show them flips the control and drops the notification: the
    // count is a property of the hidden state, not a permanent fixture.
    expect(screen.getByRole('button', { name: new RegExp(t('topology.hide_offline')) }).getAttribute('aria-pressed')).toBe('true')
    expect(screen.queryByText(t('topology.offline_hidden', { count: 1 }))).toBeNull()
  })
})
