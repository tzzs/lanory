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
import { EDGE_STATUS_COLORS, confidenceBand, formatEdgeLabel, formatConfidence, addressSummary, isMultiHomed } from '@/lib/devices'
import { t } from '@/i18n'
import { hierarchy, edgePort, segmentPort } from './hierarchy'
import type { Topology as TopologyPayload, TopologyNode as LanoryNode, TopologyEdge as LanoryEdge, NetworkInterface } from '@/types/api'

export interface TopologyNodeData extends Record<string, unknown> {
  nodeId: string
  label: string
  kind: 'device' | 'network' | 'unresolved'
  icon: string
  state: string
  stateReason?: string
  primaryIp?: string
  primaryMac?: string
  /** Compact address line: shows both sides when the node is multi-homed. */
  addressLine?: string
  /** True when the node carries addresses on more than one subnet. */
  multiHomed?: boolean
  deviceType?: string
  isManual?: boolean
  isLocked?: boolean
  demo?: boolean
  annotation?: boolean
  group?: boolean
  subtitle?: string
  memberCount?: number
  /**
   * Whether the card renders its detail lines (address, state text).
   * False in the low zoom tier, where that text would be unreadable.
   */
  showDetail?: boolean
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
  derived?: boolean
}

export interface BuildOptions {
  /** Threshold under which an edge is drawn dashed. */
  unverifiedThreshold: number
  interfaces?: NetworkInterface[]
  /**
   * Whether the cards render their detail lines. The caller picks the tier
   * with isDetailZoom(); the default is the full-detail tier.
   */
  showDetail?: boolean
}

/**
 * The zoom at which a device card stops carrying information.
 *
 * Measured on the real graph: at ~1470px wide the fitted viewport is ~0.696×,
 * which renders the 13px device name at ~9 CSS px and the 10px address at
 * ~7 CSS px; at 390px wide the fit drops to ~0.286×, i.e. ~3.7 CSS px and
 * ~2.9 CSS px. Below roughly 7 CSS px the text stops being readable, so the
 * low tier keeps only the name, the icon and the status dot, and enlarges the
 * name to compensate for the viewport scale.
 */
export const DETAIL_ZOOM = 0.55

/** True when the viewport zoom is high enough for the detailed node tier. */
export function isDetailZoom(zoom: number): boolean {
  return zoom >= DETAIL_ZOOM
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
  const tree = hierarchy(payload, options.interfaces)
  const nodes: Node<TopologyNodeData>[] = []
  const nodeIndex = new Map<string, number>()
  const showDetail = options.showDetail !== false

  payload.nodes.forEach((node: LanoryNode) => {
    const kind = node.unresolved ? 'unresolved' : node.kind === 'network' ? 'network' : 'device'
    const label = node.unresolved
      ? t('topology.unresolved_segment')
      : node.kind === 'network'
        ? node.network?.name ?? 'network'
        : node.device?.name ?? 'device'

    const data: TopologyNodeData = {
      nodeId: node.device?.id ?? node.id,
      label,
      kind,
      icon: iconKeyForDeviceType(node.device?.type),
      state: node.state,
      stateReason: node.state_reason,
      primaryIp: node.primary_ip,
      primaryMac: node.primary_mac,
      addressLine: addressSummary(node.addresses, node.primary_ip),
      multiHomed: isMultiHomed(node.addresses),
      deviceType: node.device?.type,
      isManual: node.device?.is_manual,
      isLocked: node.device?.is_locked,
      demo: node.device?.discovery_sources?.includes('demo'),
      group: tree.groups.has(node.id),
      subtitle: node.unresolved ? t('topology.segment_hint') : node.kind === 'network' ? [node.network?.cidr, node.network?.role?.toUpperCase()].filter(Boolean).join(' · ') : undefined,
      memberCount: tree.groups.get(node.id)?.length,
      showDetail,
    }

    if (node.unresolved) data.label = [segmentPort(payload, node), t('topology.unresolved_segment')].filter(Boolean).join(' · ')
    if (tree.uplink?.node === node.id) {
      const iface = tree.uplink.interface
      data.label = `WAN · ${iface?.name ?? node.network?.name ?? ''}`
      data.icon = 'cable'
      data.state = iface?.carrier ? 'online' : iface ? 'offline' : node.state
      data.subtitle = [node.network?.cidr, iface?.speed_mbps ? formatEdgeLabel({ speedMbps: iface.speed_mbps }) : undefined].filter(Boolean).join(' · ')
    }
    if (node.kind === 'network' && tree.uplink?.node !== node.id) data.icon = node.unresolved ? 'unresolved' : 'network'
    const layout = payload.layout.find((l) => l.node_id === node.id)

    nodes.push({
      id: node.id,
      type: 'lanory',
      position: tree.positions.get(node.id) ?? { x: 0, y: 0 },
      ...(tree.groups.has(node.id) ? { style: tree.size.get(node.id), zIndex: -1 } : {}),
      ...(tree.groups.has(tree.parent.get(node.id) ?? '') ? { parentId: tree.parent.get(node.id), extent: 'parent' as const } : {}),
      data,
      // Pinned positions are not draggable so a stray drag cannot lose them.
      draggable: !layout?.pinned && !(tree.groups.get(node.id) ?? []).some(id => payload.layout.some(p => p.node_id === id && p.pinned)),
    })
    nodeIndex.set(node.id, nodes.length - 1)
  })

  const edges: Edge<TopologyEdgeData>[] = []
  payload.edges.forEach((edge: LanoryEdge) => {
    if (!nodeIndex.has(edge.source_id) || !nodeIndex.has(edge.target_id)) return

    const band = confidenceBand(edge.confidence)
    const dashed = (!edge.user_confirmed && edge.edge_type === 'observed_mac_port') || edge.unverified || edge.confidence < options.unverifiedThreshold || !edge.user_confirmed && edge.edge_type !== 'physical_link' && band === 'low'

    const group = tree.parent.get(edge.target_id)
    if (edge.edge_type === 'observed_mac_port' && group && tree.groups.has(group) && !edge.user_confirmed) return
    const port = options.interfaces?.find(i => i.id === edge.source_interface_id)?.name ?? edgePort(payload, edge)
    const speed = options.interfaces?.find(i => i.id === edge.source_interface_id)?.speed_mbps || resolveSpeed(payload, edge)

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
        label: edge.label === port ? undefined : edge.label,
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
  })

  if (tree.uplink) {
    const iface = tree.uplink.interface
    const label = formatEdgeLabel({ port: iface?.name, speedMbps: iface?.speed_mbps })
    edges.push({ id: 'presentation-wan-uplink', source: tree.uplink.node, target: tree.uplink.router, type: 'smoothstep', label,
      style: { stroke: 'var(--accent)', strokeWidth: 1.5 },
      labelStyle: { fill: 'var(--muted)', fontSize: 11 }, labelBgStyle: { fill: 'var(--surface)' },
      data: { edgeId: '', edgeType: 'wan_interface', confidence: 1, confidenceBand: 'high', status: iface?.carrier ? 'link_up' : 'link_down', userConfirmed: false, locked: false, unverified: false, label, port: iface?.name, speedMbps: iface?.speed_mbps, evidenceCount: 0, derived: true },
    })
  }
  // A shared port is an observed grouping, not a confirmed switch or AP.
  for (const [id, members] of tree.groups) {
    const source = tree.parent.get(id)
    if (!source || !nodeIndex.has(source)) continue
    const port = segmentPort(payload, tree.byId.get(id)!)
    const speed = options.interfaces?.find(i => i.name === port)?.speed_mbps
    const label = formatEdgeLabel({ port, speedMbps: speed })
    edges.push({
      id: `group-${id}`, source, target: id, type: 'smoothstep',
      label,
      style: { stroke: 'var(--muted)', strokeWidth: 1.5, strokeDasharray: '5 4' },
      labelStyle: { fill: 'var(--fg)', fontSize: 11 },
      labelBgStyle: { fill: 'var(--surface)' },
      data: { edgeId: '', edgeType: 'observed_mac_port', confidence: 0, confidenceBand: 'low', status: 'unverified', userConfirmed: false, locked: false, unverified: true, label, port, speedMbps: speed, evidenceCount: members.length, derived: true },
    })
  }
  if (tree.unplaced.length && payload.nodes.some(n => n.children?.length)) {
    const first = tree.positions.get(tree.unplaced[0])!
    nodes.push({ id: 'presentation-unplaced', type: 'lanory', position: { x: first.x, y: first.y - 60 }, draggable: false, selectable: false,
      data: { nodeId: '', kind: 'network', icon: 'network', state: 'unknown', label: t('topology.unplaced'), annotation: true } })
  }
  // React Flow requires parent nodes to precede their children.
  nodes.sort((a, b) => Number(!!a.parentId) - Number(!!b.parentId))
  return { nodes, edges }
}

/**
 * Drops offline device nodes from an already-built graph.
 *
 * Two rules keep the result honest rather than merely tidy:
 *
 *   - a node that still has a visible descendant is kept, even when it is
 *     offline. Hiding a router would otherwise orphan every device behind it and
 *     silently claim those devices have no upstream;
 *   - network nodes are never dropped: a network is not online or offline, and
 *     the graph's spine would disappear with it.
 */
export function hideOffline(
  nodes: Node<TopologyNodeData>[],
  edges: Edge<TopologyEdgeData>[],
): { nodes: Node<TopologyNodeData>[]; edges: Edge<TopologyEdgeData>[]; hidden: number } {
  const kept = new Set<string>()
  const hidden = new Set<string>()
  for (const node of nodes) {
    const droppable = node.data.kind === 'device' && node.data.state === 'offline' && !node.data.annotation
    if (droppable) hidden.add(node.id)
    else kept.add(node.id)
  }
  if (hidden.size === 0) return { nodes, edges, hidden: 0 }

  // Re-admit every ancestor of a kept node: an offline parent of a visible child
  // is part of the picture, not noise.
  const parentOf = new Map<string, string>()
  for (const node of nodes) {
    if (node.parentId) parentOf.set(node.id, node.parentId)
  }
  for (const node of nodes) {
    if (!kept.has(node.id)) continue
    let ancestor = parentOf.get(node.id)
    const seen = new Set<string>()
    while (ancestor && !seen.has(ancestor)) {
      seen.add(ancestor)
      kept.add(ancestor)
      hidden.delete(ancestor)
      ancestor = parentOf.get(ancestor)
    }
  }
  // Same rule along the edge direction, for payloads that carry no parentId.
  for (const edge of edges) {
    if (kept.has(edge.target) && hidden.has(edge.source)) {
      kept.add(edge.source)
      hidden.delete(edge.source)
    }
  }

  const visibleNodes = nodes.filter((n) => kept.has(n.id))
  const visibleIDs = new Set(visibleNodes.map((n) => n.id))
  const visibleEdges = edges.filter((e) => visibleIDs.has(e.source) && visibleIDs.has(e.target))
  return { nodes: visibleNodes, edges: visibleEdges, hidden: hidden.size }
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
