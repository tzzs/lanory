/**
 * Maps a Lanory Topology payload to React Flow nodes and edges.
 *
 * Visual rules that come from the product spec:
 *  - an unverified / low-confidence relation is drawn dashed, never as a solid
 *    line, so inference is never presented as fact
 *  - the edge label carries the port name and speed, so state is not conveyed
 *    by colour alone
 *  - user-confirmed and locked edges get a distinct marker
 */

import { MarkerType, type Edge, type Node } from '@xyflow/react'
import { EDGE_STATUS_COLORS, confidenceBand, formatEdgeLabel, formatConfidence } from '@/lib/devices'
import { t } from '@/i18n'
import type { Topology as TopologyPayload, TopologyNode as LanoryNode, TopologyEdge as LanoryEdge } from '@/types/api'

export interface TopologyNodeData extends Record<string, unknown> {
  nodeId: string
  label: string
  kind: 'device' | 'network' | 'unresolved'
  icon: string
  state: string
  stateReason?: string
  primaryIp?: string
  primaryMac?: string
  deviceType?: string
  isManual?: boolean
  isLocked?: boolean
  demo?: boolean
}

export interface TopologyEdgeData extends Record<string, unknown> {
  edgeId: string
  edgeType: string
  confidence: number
  confidenceBand: 'high' | 'medium' | 'low'
  status: string
  userConfirmed: boolean
  locked: boolean
  unverified: boolean
  label: string
  port?: string
  speedMbps?: number
  evidenceCount: number
}

export interface BuildOptions {
  /** Threshold under which an edge is drawn dashed. */
  unverifiedThreshold: number
}

/** Icon key per device type, resolved to a component in the node renderer. */
export function iconKeyForDeviceType(type: string | undefined): string {
  switch (type) {
    case 'internet':
      return 'globe'
    case 'router':
      return 'router'
    case 'switch':
      return 'network'
    case 'wireless_ap':
    case 'mesh_node':
      return 'wifi'
    case 'desktop':
      return 'monitor'
    case 'laptop':
      return 'laptop'
    case 'smartphone':
      return 'smartphone'
    case 'server':
      return 'server'
    case 'nas':
      return 'hard-drive'
    case 'modem_ont':
    case 'network_fiber':
      return 'cable'
    default:
      return 'generic'
  }
}

export function buildFlow(payload: TopologyPayload, options: BuildOptions): { nodes: Node<TopologyNodeData>[]; edges: Edge<TopologyEdgeData>[] } {
  const nodes: Node<TopologyNodeData>[] = []
  const nodeIndex = new Map<string, number>()

  payload.nodes.forEach((node: LanoryNode, i: number) => {
    const kind = node.unresolved ? 'unresolved' : node.kind === 'network' ? 'network' : 'device'
    const label = node.unresolved
      ? t('topology.unresolved_segment')
      : node.kind === 'network'
        ? node.network?.name ?? 'network'
        : node.device?.name ?? 'device'

    const data: TopologyNodeData = {
      nodeId: node.id,
      label,
      kind,
      icon: iconKeyForDeviceType(node.device?.type),
      state: node.state,
      stateReason: node.state_reason,
      primaryIp: node.primary_ip,
      primaryMac: node.primary_mac,
      deviceType: node.device?.type,
      isManual: node.device?.is_manual,
      isLocked: node.device?.is_locked,
      demo: node.device?.discovery_sources?.includes('demo'),
    }

    const layout = payload.layout.find((l) => l.node_id === node.id)

    nodes.push({
      id: node.id,
      type: 'lanory',
      position: layout ? { x: layout.x, y: layout.y } : { x: (i % 4) * 220, y: Math.floor(i / 4) * 140 },
      data,
      // Pinned positions are not draggable so a stray drag cannot lose them.
      draggable: !layout?.pinned,
    })
    nodeIndex.set(node.id, nodes.length - 1)
  })

  const edges: Edge<TopologyEdgeData>[] = []
  payload.edges.forEach((edge: LanoryEdge) => {
    if (!nodeIndex.has(edge.source_id) || !nodeIndex.has(edge.target_id)) return

    const band = confidenceBand(edge.confidence)
    const dashed = edge.unverified || edge.confidence < options.unverifiedThreshold || !edge.user_confirmed && edge.edge_type !== 'physical_link' && band === 'low'

    const sourceNode = payload.nodes.find((n) => n.id === edge.source_id)
    const port = edge.source_interface_id ?? edge.target_interface_id
    const speed = resolveSpeed(payload, edge)

    const data: TopologyEdgeData = {
      edgeId: edge.id,
      edgeType: edge.edge_type,
      confidence: edge.confidence,
      confidenceBand: band,
      status: edge.status,
      userConfirmed: edge.user_confirmed,
      locked: edge.locked,
      unverified: edge.unverified,
      label: formatEdgeLabel({
        port,
        speedMbps: speed,
        label: edge.label,
      }),
      port,
      speedMbps: speed,
      evidenceCount: edge.evidence_count,
    }

    const stroke =
      edge.status === 'link_down' || edge.status === 'unreachable'
        ? EDGE_STATUS_COLORS.unreachable
        : edge.status === 'link_up' || edge.status === 'reachable'
          ? EDGE_STATUS_COLORS.link_up
          : EDGE_STATUS_COLORS.unverified

    edges.push({
      id: edge.id,
      source: edge.source_id,
      target: edge.target_id,
      type: 'smoothstep',
      data,
      label: data.label || undefined,
      labelStyle: { fill: 'var(--muted)', fontSize: 10, fontFamily: 'ui-monospace, monospace' },
      labelBgStyle: { fill: 'var(--surface)', fillOpacity: 0.9 },
      labelBgPadding: [4, 2] as [number, number],
      labelBgBorderRadius: 3,
      style: {
        stroke,
        strokeWidth: edge.user_confirmed ? 2 : 1.5,
        strokeDasharray: dashed ? '5 4' : undefined,
      },
      markerEnd: {
        type: MarkerType.ArrowClosed,
        width: 14,
        height: 14,
        color: stroke,
      },
      animated: edge.status === 'reachable',
    })
    void sourceNode
  })

  return { nodes, edges }
}

/** Finds the speed of the interface referenced by an edge, if any. */
function resolveSpeed(payload: TopologyPayload, edge: LanoryEdge): number | undefined {
  const ids = [edge.source_interface_id, edge.target_interface_id].filter(Boolean) as string[]
  for (const node of payload.nodes) {
    if (node.interface && ids.includes(node.interface.id)) return node.interface.speed_mbps || undefined
    if (node.device) {
      // Interfaces are not embedded in the node payload; fall back to nothing.
      continue
    }
  }
  return undefined
}

/** Formats the confidence for the edge inspector. */
export { formatConfidence }
