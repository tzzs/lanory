import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  ReactFlow,
  ReactFlowProvider,
  Background,
  Controls,
  MiniMap,
  useReactFlow,
  type Node,
  type Edge,
  type NodeMouseHandler,
  type EdgeMouseHandler,
  type OnNodesChange,
  type OnEdgesChange,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { LayoutGrid, Save, Maximize2, Plus, Link2, RefreshCw } from 'lucide-react'
import { t } from '@/i18n'
import { useTopology, useSaveLayout, useAutoLayout, useCreateEdge } from '@/hooks/useApi'
import { useUIStore } from '@/stores/ui'
import { Button, Card, Badge, Modal, Select, Input, EmptyState, ErrorState, Skeleton } from '@/components/ui'
import { LanoryNode } from '@/features/topology/LanoryNode'
import { buildFlow, type TopologyNodeData, type TopologyEdgeData } from '@/features/topology/graph'
import { AddDeviceForm } from '@/features/devices/AddDeviceForm'
import { EdgeInspector } from '@/features/topology/EdgeInspector'
import { DeviceInspector } from '@/features/topology/DeviceInspector'
import type { LayoutNode } from '@/types/api'

const nodeTypes = { lanory: LanoryNode }

const UNVERIFIED_THRESHOLD = 0.6

function TopologyCanvas() {
  const { data, isLoading, isError, error, refetch } = useTopology()
  const saveLayout = useSaveLayout()
  const autoLayout = useAutoLayout()
  const selectDevice = useUIStore((s) => s.selectDevice)
  const selectEdge = useUIStore((s) => s.selectEdge)
  const selectedEdgeId = useUIStore((s) => s.selectedEdgeId)
  const setDetailPanelOpen = useUIStore((s) => s.setDetailPanelOpen)

  const [nodes, setNodes] = useState<Node<TopologyNodeData>[]>([])
  const [edges, setEdges] = useState<Edge<TopologyEdgeData>[]>([])
  const [addOpen, setAddOpen] = useState(false)
  const [addLinkOpen, setAddLinkOpen] = useState(false)
  const [dirty, setDirty] = useState(false)
  const [algorithm, setAlgorithm] = useState<'tree' | 'layered' | 'force'>('tree')

  const { fitView } = useReactFlow()
  const hasFitted = useRef(false)

  // Rebuild the graph whenever the payload changes.
  useEffect(() => {
    if (!data) return
    const built = buildFlow(data, { unverifiedThreshold: UNVERIFIED_THRESHOLD })
    setNodes(built.nodes)
    setEdges(built.edges)
    setDirty(false)
  }, [data])

  // Fit the view once on first data, but never again so the user's zoom and
  // pan survive subsequent polls.
  useEffect(() => {
    if (!data || hasFitted.current || nodes.length === 0) return
    hasFitted.current = true
    const raf = requestAnimationFrame(() => {
      fitView({ padding: 0.15, duration: 240 })
    })
    return () => cancelAnimationFrame(raf)
  }, [data, nodes.length, fitView])

  const onNodesChange: OnNodesChange = useCallback((changes) => {
    setNodes((current) => {
      const next = current.map((node) => {
        const change = changes.find((c) => c.type === 'position' && c.id === node.id)
        if (change && change.type === 'position' && change.position) {
          return { ...node, position: change.position }
        }
        if (change && change.type === 'select') {
          return { ...node, selected: change.selected }
        }
        return node
      })
      if (changes.some((c) => c.type === 'position')) setDirty(true)
      return next
    })
  }, [])

  const onEdgesChange: OnEdgesChange = useCallback((changes) => {
    setEdges((current) =>
      current.map((edge) => {
        const change = changes.find((c) => c.type === 'select' && c.id === edge.id)
        if (change && change.type === 'select') return { ...edge, selected: change.selected }
        return edge
      }),
    )
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
      selectEdge(data.edgeId)
      setDetailPanelOpen(true)
    },
    [selectEdge, setDetailPanelOpen],
  )

  const onSaveLayout = useCallback(() => {
    const payload: LayoutNode[] = nodes.map((node) => ({
      node_id: node.id,
      x: node.position.x,
      y: node.position.y,
      pinned: !node.draggable,
    }))
    saveLayout.mutate({ nodes: payload })
    setDirty(false)
  }, [nodes, saveLayout])

  const onAutoLayout = useCallback(async () => {
    const result = await autoLayout.mutateAsync({ algorithm })
    const positions = new Map(result.layout.map((l) => [l.node_id, l]))
    setNodes((current) =>
      current.map((node) => {
        const pos = positions.get(node.id)
        return pos ? { ...node, position: { x: pos.x, y: pos.y } } : node
      }),
    )
    setDirty(true)
    const raf = requestAnimationFrame(() => fitView({ padding: 0.15, duration: 240 }))
    return () => cancelAnimationFrame(raf)
  }, [algorithm, autoLayout, fitView])

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

  if (isError) {
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
        {/* toolbar */}
        <div className="absolute left-4 top-4 z-10 flex flex-wrap items-center gap-2">
          <Select
            className="w-auto min-w-[130px] bg-surface"
            value={algorithm}
            onChange={(e) => setAlgorithm(e.target.value as typeof algorithm)}
            aria-label={t('topology.auto_layout')}
          >
            <option value="tree">{t('topology.layout_tree')}</option>
            <option value="layered">{t('topology.layout_layered')}</option>
            <option value="force">{t('topology.layout_force')}</option>
          </Select>
          <Button size="sm" onClick={() => void onAutoLayout()} disabled={autoLayout.isPending}>
            <LayoutGrid size={14} aria-hidden="true" />
            {t('topology.auto_layout')}
          </Button>
          <Button size="sm" onClick={onSaveLayout} disabled={!dirty || saveLayout.isPending}>
            <Save size={14} aria-hidden="true" />
            {saveLayout.isPending ? t('app.loading') : t('app.save')}
          </Button>
          <Button size="sm" variant="ghost" onClick={() => fitView({ padding: 0.15, duration: 240 })}>
            <Maximize2 size={14} aria-hidden="true" />
            {t('topology.fit_view')}
          </Button>
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
        </div>

        <ReactFlow
          nodes={nodes}
          edges={edges}
          nodeTypes={nodeTypes}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onNodeClick={onNodeClick}
          onEdgeClick={onEdgeClick}
          nodesDraggable
          nodesConnectable={false}
          elementsSelectable
          minZoom={0.2}
          maxZoom={2.5}
          proOptions={{ hideAttribution: true }}
          defaultEdgeOptions={{ type: 'smoothstep' }}
        >
          <Background gap={16} size={1} color="var(--border)" />
          <Controls showInteractive={false} className="!bg-surface !border-border" />
          <MiniMap
            pannable
            zoomable
            className="!bg-surface"
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

      {selectedEdge ? <EdgeInspector edge={selectedEdge} /> : <DeviceInspector />}

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
      <div className="space-y-3">
        <label className="block">
          <span className="text-xs text-muted">{t('devices.merge_target')}</span>
          <Select className="mt-1" value={sourceId} onChange={(e) => setSourceId(e.target.value)}>
            <option value="">{t('app.none')}</option>
            {devices.map((node) => (
              <option key={node.id} value={node.id}>
                {node.device?.name ?? node.id}
              </option>
            ))}
          </Select>
        </label>
        <label className="block">
          <span className="text-xs text-muted">{t('devices.merge_source')}</span>
          <Select className="mt-1" value={targetId} onChange={(e) => setTargetId(e.target.value)}>
            <option value="">{t('app.none')}</option>
            {devices.map((node) => (
              <option key={node.id} value={node.id}>
                {node.device?.name ?? node.id}
              </option>
            ))}
          </Select>
        </label>
        <label className="block">
          <span className="text-xs text-muted">{t('device.edges')}</span>
          <Select className="mt-1" value={edgeType} onChange={(e) => setEdgeType(e.target.value)}>
            <option value="user_defined">{t('edge.user_defined')}</option>
            <option value="physical_link">{t('edge.physical_link')}</option>
            <option value="wireless_assoc">{t('edge.wireless_assoc')}</option>
          </Select>
        </label>
        <label className="block">
          <span className="text-xs text-muted">{t('device.interface')}</span>
          <Input className="mt-1 font-mono" value={label} onChange={(e) => setLabel(e.target.value)} placeholder="eth1" />
        </label>
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
      <ReactFlowProvider>
        <TopologyCanvas />
      </ReactFlowProvider>
    </div>
  )
}

