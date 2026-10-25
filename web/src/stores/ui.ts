/**
 * Zustand store for UI state that must be shared across routes but is not
 * server data (which belongs to TanStack Query).
 *
 * Kept deliberately small: selected device, detail-panel visibility, theme,
 * language and the demo banner.
 */

import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import type { Device, TopologyEdge, DeviceState, DeviceType } from '@/types/api'
import { detectLanguage, setLanguage, type Language } from '@/i18n'

export type ThemeMode = 'light' | 'dark' | 'system'

interface UIState {
  /* theme + language */
  theme: ThemeMode
  setTheme: (theme: ThemeMode) => void
  language: Language
  setLang: (lang: Language) => void

  /* selection */
  selectedDeviceId: string | null
  selectDevice: (id: string | null) => void
  selectedDevice: Device | null
  setSelectedDevice: (device: Device | null) => void

  selectedEdgeId: string | null
  selectEdge: (id: string | null) => void
  selectedEdge: TopologyEdge | null
  setSelectedEdge: (edge: TopologyEdge | null) => void

  detailPanelOpen: boolean
  setDetailPanelOpen: (open: boolean) => void

  // Global filters shared by the device table and the topology view.
  // (Written as a line comment: `/* global ... */` is an ESLint directive that
  // declares globals, which produced nine spurious unused-var warnings here.)
  deviceFilters: {
    query: string
    type: DeviceType | ''
    state: DeviceState | ''
    source: string
    iface: string
    newOnly: boolean
  }
  setDeviceFilter: <K extends keyof UIState['deviceFilters']>(key: K, value: UIState['deviceFilters'][K]) => void
  resetDeviceFilters: () => void

  /* demo mode */
  demoMode: boolean
  setDemoMode: (demo: boolean) => void

  /* realtime connection state for the status indicator */
  realtimeConnected: boolean
  setRealtimeConnected: (connected: boolean) => void
}

const defaultFilters: UIState['deviceFilters'] = {
  query: '',
  type: '',
  state: '',
  source: '',
  iface: '',
  newOnly: false,
}

export const useUIStore = create<UIState>()(
  persist(
    (set) => ({
      theme: 'system',
      setTheme: (theme) => set({ theme }),

      // Seeded from the same detection the i18n module uses, so the Settings page
      // highlights the language that is actually being rendered. Hardcoding 'en'
      // made a Chinese UI claim English was selected.
      language: detectLanguage(),
      setLang: (language) => {
        setLanguage(language)
        set({ language })
      },

      selectedDeviceId: null,
      selectDevice: (id) => set({ selectedDeviceId: id, selectedEdgeId: null, detailPanelOpen: id !== null }),
      selectedDevice: null,
      setSelectedDevice: (device) =>
        set({ selectedDevice: device, selectedDeviceId: device?.id ?? null, detailPanelOpen: device !== null }),

      selectedEdgeId: null,
      selectEdge: (id) => set({ selectedEdgeId: id, selectedDeviceId: null, detailPanelOpen: id !== null }),
      selectedEdge: null,
      setSelectedEdge: (edge) => set({ selectedEdge: edge, selectedEdgeId: edge?.id ?? null, detailPanelOpen: edge !== null }),

      detailPanelOpen: false,
      setDetailPanelOpen: (open) => set({ detailPanelOpen: open }),

      deviceFilters: defaultFilters,
      setDeviceFilter: (key, value) =>
        set((state) => ({ deviceFilters: { ...state.deviceFilters, [key]: value } })),
      resetDeviceFilters: () => set({ deviceFilters: defaultFilters }),

      demoMode: false,
      setDemoMode: (demoMode) => set({ demoMode }),

      realtimeConnected: false,
      setRealtimeConnected: (realtimeConnected) => set({ realtimeConnected }),
    }),
    {
      name: 'lanory.ui',
      // Only preferences are persisted; selection is per-session.
      partialize: (state) => ({
        theme: state.theme,
        language: state.language,
        deviceFilters: state.deviceFilters,
      }),
      // The i18n module keeps its own copy of the language for `t()`; push the
      // rehydrated value into it on boot, otherwise a persisted 'zh-CN' never
      // reaches the dictionary (or `<html lang>`) until the user clicks.
      // `persist` calls this synchronously for the localStorage backend, i.e.
      // before the store module finishes evaluating, so the first render already
      // has the right language.
      onRehydrateStorage: () => (state) => {
        if (state) setLanguage(state.language)
      },
    },
  ),
)
