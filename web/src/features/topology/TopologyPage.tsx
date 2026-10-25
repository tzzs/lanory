import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  ReactFlow,
  ReactFlowProvider,
  Background,
  Controls,
  MiniMap,
  useReactFlow,
  useNodesInitialized,
  applyNodeChanges,
  applyEdgeChanges,
  type Node,
  type Edge,
  type NodeMouseHandler,
  type EdgeMouseHandler,
  type OnNodesChange,
  type OnEdgesChange,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { LayoutGrid, Save, Maximize2, Plus, Link2, RefreshCw, Eye } from 'lucide-react'
import { t } from '@/i18n'
import { useTopology, useSaveLayout, useCreateEdge, useMonitoring } from '@/hooks/useApi'
import { useUIStore } from '@/stores/ui'
import { Button, Card, Badge, Modal, Select, Input, EmptyState, ErrorState, Skeleton, Field } from '@/components/ui'
import { LanoryNode } from '@/features/topology/LanoryNode'
import { buildFlow, hideOffline, isDetailZoom, type TopologyNodeData, type TopologyEdgeData } from '@/features/topology/graph'
import { AddDeviceForm } from '@/features/devices/AddDeviceForm'
import { EdgeInspector } from '@/features/topology/EdgeInspector'
import { DeviceInspector } from '@/features/topology/DeviceInspector'
import type { LayoutNode } from '@/types/api'

const nodeTypes = { lanory: LanoryNode }

const UNVERIFIED_THRESHOLD = 0.6

/**
 * The theme the app is *rendering* with, resolved from the persisted mode.
 *
 * `'system'` is resolved against the same media query `lib/theme.ts` uses to
 * put `dark` on the root element: React Flow's own `'system'` watches a
 * different class (`react-flow.dark`, driven by its own `colorMode` prop) than
 * the app's class-based dark mode, so letting React Flow resolve it would let
 * the two disagree. The store value alone is not enough either — a `'system'
 * mode in a browser set to light must render the light controls.
 */
function useResolvedColorMode(): 'light' | 'dark' {
  const mode = useUIStore((s) => s.theme)
  const [systemDark, setSystemDark] = useState(
    () => typeof window !== 'undefined' && !!window.matchMedia?.('(prefers-color-scheme: dark)').matches,
  )
  useEffect(() => {
    const media = window.matchMedia?.('(prefers-color-scheme: dark)')
    if (!media) return
    const onChange = (e: MediaQueryListEvent) => setSystemDark(e.matches)
    media.addEventListener('change', onChange)
    return () => media.removeEventListener('change', onChange)
  }, [])
  return mode === 'system' ? (systemDark ? 'dark' : 'light') : mode
}

function TopologyCanvas() {
  const { data, isLoading, isError, error, refetch } = useTopology()
  const saveLayout = useSaveLayout()
  const monitoring = useMonitoring()
  const colorMode = useResolvedColorMode()
  const selectDevice = useUIStore((s) => s.selectDevice)
  const selectEdge = useUIStore((s) => s.selectEdge)
  const selectedEdgeId = useUIStore((s) => s.selectedEdgeId)
  const setDetailPanelOpen = useUIStore((s) => s.setDetailPanelOpen)

  const [nodes, setNodes] = useState<Node<TopologyNodeData>[]>([])
  const [edges, setEdges] = useState<Edge<TopologyEdgeData>[]>([])
  const [addOpen, setAddOpen] = useState(false)
  const [addLinkOpen, setAddLinkOpen] = useState(false)
  const [dirty, setDirty] = useState(false)
  const initialized = useNodesInitialized()
  const detailPanelOpen = useUIStore(s => s.detailPanelOpen)
  // Offline devices are hidden by default: the map answers "what is on my
  // network now", and a wall of red boxes from devices that are asleep drowns
  // that answer. The toggle and the hidden-count badge keep them discoverable,
  // so nothing is silently lost.
  const [showOffline, setShowOffline] = useState(false)
  const [hiddenOffline, setHiddenOffline] = useState(0)
  // The viewport zoom decides how much of a card can be read: below the
  // detail threshold the node renders in its low tier (name only, bigger
  // type). The raw zoom is tracked, but only the tier is allowed to reach
  // the graph, so a wheel gesture inside one tier rebuilds nothing.
  const [zoom, setZoom] = useState(1)
  const showDetail = useMemo(() => isDetailZoom(zoom), [zoom])

  const { fitView } = useReactFlow()
  const hasFitted = useRef(false)

  // Build the graph from the payload. The build is memoized on the tier
  // rather than on the raw zoom, so the graph is only rebuilt when the tier
  // actually flips.
  const built = useMemo(() => {
    if (!data) return null
    const flow = buildFlow(data, {
      unverifiedThreshold: UNVERIFIED_THRESHOLD,
      interfaces: monitoring.data?.interfaces.map(s => s.interface),
      showDetail,
    })
    return showOffline ? { ...flow, hidden: 0 } : hideOffline(flow.nodes, flow.edges)
  }, [data, monitoring.data, showOffline, showDetail])

  // Rebuild the graph whenever the payload changes.
  useEffect(() => {
    if (!built || dirty) return
    setNodes(built.nodes)
    setEdges(built.edges)
    setHiddenOffline(built.hidden)
    setDirty(false)
  }, [built, dirty])

  // Apply the tier to the nodes already on screen. A graph with unsaved
  // layout changes is never rebuilt from the payload, so the tier is
  // applied as a data patch instead of a rebuild.
  useEffect(() => {
    setNodes(current => {
      if (current.length === 0 || current.every(n => n.data.showDetail === showDetail)) return current
      return current.map(n => (n.data.showDetail === showDetail ? n : { ...n, data: { ...n.data, showDetail } }))
    })
  }, [showDetail])

  // Fit the view once on first data, but never again so the user's zoom and
  // pan survive subsequent polls.
  useEffect(() => {
    if (!data || !initialized || hasFitted.current || nodes.length === 0) return
    const raf = requestAnimationFrame(() => {
      const connected = nodes.filter(n => n.parentId || n.data.group || edges.some(e => e.source === n.id || e.target === n.id))
      void fitView({ nodes: connected.length ? connected : nodes, padding: 0.15, duration: 0 }).then(() => { hasFitted.current = true })
    })
    return () => cancelAnimationFrame(raf)
  }, [data, initialized, nodes, edges, fitView])

  const onNodesChange: OnNodesChange<Node<TopologyNodeData>> = useCallback((changes) => {
    setNodes(current => applyNodeChanges(changes, current))
    if (changes.some(c => c.type === 'position' && c.dragging)) setDirty(true)
  }, [])

  const onEdgesChange: OnEdgesChange<Edge<TopologyEdgeData>> = useCallback((changes) => {
    setEdges(current => applyEdgeChanges(changes, current))
  }, [])

  // The zoom is tracked so the tier can be derived; the graph itself is only
  // rebuilt when the tier flips, not on every wheel tick.
  const onMove = useCallback((_event: MouseEvent | TouchEvent | null, viewport: { zoom: number }) => {
    setZoom(viewport.zoom)
  }, [])

  const onNodeClick: NodeMouseHandler = useCallback(
    (_event, node) => {
      const data = node.data as TopologyNodeData
      if (data.kind === 'network' || data.kind === 'unresolved') return
      selectDevice(data.nodeId)
      setDetailPanelOpen(true)
    },
    [selectDevice, setDetailPanelOpen],
  )

  const onEdgeClick: EdgeMouseHandler = useCallback(
    (_event, edge) => {
      const data = edge.data as TopologyEdgeData
      if (data.derived) return
      selectEdge(data.edgeId)
      setDetailPanelOpen(true)
    },
    [selectEdge, setDetailPanelOpen],
  )

  const onSaveLayout = useCallback(() => {
    const payload: LayoutNode[] = nodes.filter(n => !n.data.annotation).map((node) => ({
      node_id: node.id,
      x: node.position.x + (nodes.find(n => n.id === node.parentId)?.position.x ?? 0),
      y: node.position.y + (nodes.find(n => n.id === node.parentId)?.position.y ?? 0),
      pinned: !node.draggable,
    }))
    saveLayout.mutate({ nodes: payload }, { onSuccess: () => setDirty(false) })
  }, [nodes, saveLayout])

  const onAutoLayout = useCallback(() => {
    if (!data) return
    hasFitted.current = false
    const flow = buildFlow({ ...data, layout: data.layout.filter(p => p.pinned) }, {
      unverifiedThreshold: UNVERIFIED_THRESHOLD,
      interfaces: monitoring.data?.interfaces.map(s => s.interface),
      showDetail,
    })
    const visible = showOffline ? { ...flow, hidden: 0 } : hideOffline(flow.nodes, flow.edges)
    setNodes(visible.nodes)
    setEdges(visible.edges)
    setHiddenOffline(visible.hidden)
    setDirty(true)
  }, [data, monitoring.data, showOffline, showDetail])

  const unplaced = nodes.filter(n => n.data.kind === 'device' && !n.parentId && !edges.some(e => e.source === n.id || e.target === n.id) && !data?.nodes.find(item => item.id === n.id)?.device?.is_self)

  const selectedEdge = useMemo(
    () => edges.find((e) => e.id === selectedEdgeId) ?? null,
    [edges, selectedEdgeId],
  )

  if (isLoading) {
    return (
      <div className="h-full p-6">
        <Skeleton className="h-full w-full" />
      </div>
    )
  }

  if (isError && !data) {
    return (
      <ErrorState
        title={t('app.error')}
        description={error instanceof Error ? error.message : undefined}
        action={
          <Button variant="primary" onClick={() => void refetch()}>
            {t('app.retry')}
          </Button>
        }
      />
    )
  }

  if (nodes.length === 0) {
    return (
      <div className="h-full p-6">
        <Card className="h-full">
          <EmptyState
            title={t('topology.empty')}
            description={t('discovery.scan')}
            action={
              <Button variant="primary" onClick={() => void refetch()}>
                <RefreshCw size={14} aria-hidden="true" />
                {t('app.refresh')}
              </Button>
            }
          />
        </Card>
      </div>
    )
  }

  return (
    <div className="flex h-full min-h-0">
      <div className="relative min-w-0 flex-1">
        {/* Overlays live in the normal flow above the canvas, not pinned over it:
            at 390px a wrapped 2-3 row toolbar used to collide with the badge,
            the hint and the save error, and the error banner sat on top of the
            toolbar's second row. The stack below is what the toolbar's height
            actually is, so the banner can never overlap it. */}
        <div data-testid="topology-overlays" className="absolute inset-x-0 top-0 z-10 flex flex-col items-end gap-2 p-4">
          <div className="pointer-events-none w-full">
            {/* the hint is decoration, not content: below `sm` it covers the
                canvas it is describing, so it is dropped there. */}
            <div className="hidden sm:block max-w-sm rounded-md border border-border bg-surface/95 px-3 py-2 text-[11px] text-muted">{t('topology.hierarchy_hint')}</div>
          </div>

          {/* toolbar. `w-full` + `flex-wrap` bounds it to the overlay
              column (the canvas minus its p-4 gutter), so at 390px the buttons
              wrap onto more rows instead of running off screen. */}
          <div className="flex w-full max-w-full flex-wrap items-center gap-2">
            <Button size="sm" onClick={onAutoLayout}>
              <LayoutGrid size={14} aria-hidden="true" />
              {t('topology.reset_layout')}
            </Button>
            <Button size="sm" onClick={onSaveLayout} disabled={!dirty || saveLayout.isPending}>
              <Save size={14} aria-hidden="true" />
              {saveLayout.isPending ? t('app.loading') : t('app.save')}
            </Button>
            <Button size="sm" variant="ghost" onClick={() => fitView({ padding: 0.15, duration: 240 })}>
              <Maximize2 size={14} aria-hidden="true" />
              {t('topology.fit_view')}
            </Button>
            {unplaced.length ? <Button size="sm" variant="ghost" onClick={() => void fitView({ nodes: unplaced, padding: 0.2, duration: 0 })}>{t('topology.view_unmapped', { count: unplaced.length })}</Button> : null}
            <div className="mx-1 h-5 w-px bg-border" aria-hidden="true" />
            <Button size="sm" variant="ghost" onClick={() => setAddOpen(true)}>
              <Plus size={14} aria-hidden="true" />
              {t('topology.add_device')}
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setAddLinkOpen(true)}>
              <Link2 size={14} aria-hidden="true" />
              {t('topology.add_link')}
            </Button>
            {dirty ? (
              <Badge tone="warning" className="ml-1">
                {t('topology.manual_layout')}
              </Badge>
            ) : null}
            {!showDetail ? (
              <span className="text-[11px] text-muted">{t('topology.zoom_for_detail')}</span>
            ) : null}
            <div className="mx-1 h-5 w-px bg-border" aria-hidden="true" />
            <Button
              size="sm"
              variant="ghost"
              onClick={() => setShowOffline((v) => !v)}
              aria-pressed={showOffline}
              title={showOffline ? t('topology.hide_offline') : t('topology.show_offline')}
            >
              <Eye size={14} aria-hidden="true" className={showOffline ? '' : 'opacity-40'} />
              {showOffline ? t('topology.hide_offline') : t('topology.show_offline')}
              {!showOffline && hiddenOffline > 0 ? (
                <span className="ml-1 rounded bg-surface-2 px-1 text-[10px] text-muted">{hiddenOffline}</span>
              ) : null}
            </Button>
          </div>

          {/* the hidden-offline count and the save error are stacked under the
              toolbar, so a 2-3 row toolbar pushes them down instead of under it. */}
          {!showOffline && hiddenOffline > 0 ? (
            <div className="flex items-center gap-2 rounded-md border border-border bg-surface/95 px-2.5 py-1.5 text-[11px] text-muted">
              <span>{t('topology.offline_hidden', { count: hiddenOffline })}</span>
              <button
                type="button"
                className="font-medium text-accent underline-offset-2 hover:underline"
                onClick={() => setShowOffline(true)}
              >
                {t('topology.offline_hidden_show')}
              </button>
            </div>
          ) : null}

          {saveLayout.isError ? <div role="alert" className="max-w-full rounded-md border border-danger/30 bg-surface px-3 py-2 text-xs text-danger">{saveLayout.error instanceof Error ? saveLayout.error.message : t('app.error')}</div> : null}
        </div>

        <ReactFlow
          nodes={nodes}
          edges={edges}
          nodeTypes={nodeTypes}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onNodeClick={onNodeClick}
          onEdgeClick={onEdgeClick}
          onMove={onMove}
          nodesDraggable
          nodesConnectable={false}
          elementsSelectable
          colorMode={colorMode}
          minZoom={0.2}
          maxZoom={2.5}
          proOptions={{ hideAttribution: true }}
          defaultEdgeOptions={{ type: 'smoothstep' }}
        >
          <Background gap={16} size={1} color="var(--border)" />
          {/* React Flow only re-themes its own panels from `colorMode` (above),
              and that palette is its own greys, not the app's tokens. The
              controls' icon colour comes from `currentColor`, so it inherits
              whatever the buttons' text colour is: the surface/border
              overrides keep the panel inside the design system, and the
              colour override keeps the icons legible on it in both themes. */}
          <Controls
            showInteractive={false}
            className="!border-border !bg-surface"
            style={{ '--xy-controls-button-color': 'var(--fg)' } as React.CSSProperties}
          />
          <MiniMap
            pannable
            zoomable
            className="!border-border"
            style={{ '--xy-minimap-background-color': 'var(--surface)' } as React.CSSProperties}
            maskColor="rgba(0,0,0,0.06)"
            nodeColor={(node) => {
              const data = node.data as TopologyNodeData
              if (data.state === 'online') return 'var(--success)'
              if (data.state === 'offline') return 'var(--danger)'
              if (data.state === 'sleeping') return 'var(--warning)'
              return 'var(--muted)'
            }}
          />
        </ReactFlow>
      </div>

      {detailPanelOpen ? selectedEdge ? <EdgeInspector edge={selectedEdge} /> : <DeviceInspector /> : null}

      <Modal
        open={addOpen}
        onClose={() => setAddOpen(false)}
        title={t('topology.add_device')}
        description={t('devices.add')}
        footer={
          <>
            <Button variant="ghost" onClick={() => setAddOpen(false)}>
              {t('app.cancel')}
            </Button>
            <Button form="topology-add-device" type="submit" variant="primary">
              {t('app.save')}
            </Button>
          </>
        }
      >
        <AddDeviceForm formId="topology-add-device" onDone={() => setAddOpen(false)} />
      </Modal>

      <AddLinkModal open={addLinkOpen} onClose={() => setAddLinkOpen(false)} />
    </div>
  )
}

/* -------------------------------------------------------------------------- */
/* Add link modal                                                             */
/* -------------------------------------------------------------------------- */

function AddLinkModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { data } = useTopology()
  const createEdge = useCreateEdge()
  const [sourceId, setSourceId] = useState('')
  const [targetId, setTargetId] = useState('')
  const [edgeType, setEdgeType] = useState('user_defined')
  const [label, setLabel] = useState('')

  const devices = (data?.nodes ?? []).filter((n) => n.kind === 'device')

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t('topology.add_link')}
      description={t('edge.user_defined')}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            {t('app.cancel')}
          </Button>
          <Button
            variant="primary"
            disabled={!sourceId || !targetId || sourceId === targetId || createEdge.isPending}
            onClick={() => {
              createEdge.mutate(
                {
                  source_id: sourceId,
                  target_id: targetId,
                  edge_type: edgeType as 'user_defined',
                  label: label || undefined,
                },
                { onSuccess: () => onClose() },
              )
            }}
          >
            {t('app.save')}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Field label={t('devices.merge_target')}>
          <Select className="" value={sourceId} onChange={(e) => setSourceId(e.target.value)}>
            <option value="">{t('app.none')}</option>
            {devices.map((node) => (
              <option key={node.id} value={node.id}>
                {node.device?.name ?? node.id}
              </option>
            ))}
          </Select>
        </Field>
        <Field label={t('devices.merge_source')}>
          <Select className="" value={targetId} onChange={(e) => setTargetId(e.target.value)}>
            <option value="">{t('app.none')}</option>
            {devices.map((node) => (
              <option key={node.id} value={node.id}>
                {node.device?.name ?? node.id}
              </option>
            ))}
          </Select>
        </Field>
        <Field label={t('device.edges')}>
          <Select className="" value={edgeType} onChange={(e) => setEdgeType(e.target.value)}>
            <option value="user_defined">{t('edge.user_defined')}</option>
            <option value="physical_link">{t('edge.physical_link')}</option>
            <option value="wireless_assoc">{t('edge.wireless_assoc')}</option>
          </Select>
        </Field>
        <Field label={t('device.interface')} description="Optional">
          <Input className="font-mono" value={label} onChange={(e) => setLabel(e.target.value)} placeholder="eth1" />
        </Field>
      </div>
    </Modal>
  )
}

/* -------------------------------------------------------------------------- */
/* Page                                                                       */
/* -------------------------------------------------------------------------- */

export function TopologyPage() {
  return (
    <div className="h-full">
      <h1 className="sr-only">{t('topology.title')}</h1>
      <ReactFlowProvider>
        <TopologyCanvas />
      </ReactFlowProvider>
    </div>
  )
}

