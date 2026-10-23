import type { Topology, TopologyNode, NetworkInterface } from '@/types/api'

export const NODE_WIDTH = 200
const COLUMN = 228
const ROW = 112

/** A presentation hierarchy uses the engine's children, never fabricated devices. */
export function hierarchy(input: Topology, interfaces: NetworkInterface[] = []) {
  const router = input.nodes.find(n => n.kind === 'device' && n.device?.is_self)
  const wanInterfaces = interfaces.filter(i => i.role === 'wan' && (!router || i.device_id === router.device?.id))
  const wanInterface = wanInterfaces.find(i => i.carrier) ?? wanInterfaces[0]
  const wanNode = input.nodes.find(n => n.kind === 'network' && !n.unresolved &&
    (wanInterface ? n.interface?.id === wanInterface.id || n.network?.interface_id === wanInterface.id : n.network?.role === 'wan'))
  const uplink = router && wanNode ? { router: router.id, node: wanNode.id, interface: wanInterface ?? wanNode.interface } : undefined
  // This is the router's known WAN interface, not an invented upstream device.
  const payload = uplink ? { ...input, nodes: input.nodes.map(n => n.id === uplink.node
    ? { ...n, children: [...new Set([...(n.children ?? []), uplink.router])] } : n) } : input
  const byId = new Map(payload.nodes.map(n => [n.id, n]))
  const parent = new Map<string, string>()
  for (const n of payload.nodes) for (const child of n.children ?? []) {
    if (byId.has(child) && child !== n.id) parent.set(child, n.id)
  }
  // Older payloads may not supply children. Keep observed directions as fallback.
  for (const e of payload.edges) {
    if (!parent.has(e.target_id) && byId.has(e.source_id) && byId.has(e.target_id) && e.target_id !== e.source_id) {
      let ancestor: string | undefined = e.source_id
      const seen = new Set<string>()
      while (ancestor && !seen.has(ancestor) && ancestor !== e.target_id) {
        seen.add(ancestor)
        ancestor = parent.get(ancestor)
      }
      if (ancestor !== e.target_id) parent.set(e.target_id, e.source_id)
    }
  }
  const groups = new Map<string, string[]>()
  for (const n of payload.nodes) {
    const members = (n.children ?? []).filter(id => byId.has(id) && parent.get(id) === n.id && !(byId.get(id)?.children?.length))
    if (n.unresolved && members.length) groups.set(n.id, members.sort())
  }
  const members = new Set([...groups.values()].flat())
  const children = new Map<string, string[]>()
  for (const [child, p] of parent) {
    if (!members.has(child)) children.set(p, [...(children.get(p) ?? []), child].sort())
  }
  const size = new Map<string, { width: number; height: number }>()
  for (const n of payload.nodes) {
    const count = groups.get(n.id)?.length ?? 0
    const cols = Math.min(4, count)
    size.set(n.id, count ? { width: cols * COLUMN + 16, height: 86 + Math.ceil(count / cols) * ROW } : { width: NODE_WIDTH, height: 88 })
  }
  const positions = new Map<string, { x: number; y: number }>()
  const span = (id: string, seen = new Set<string>()): number => {
    if (seen.has(id)) return NODE_WIDTH
    const next = new Set(seen).add(id)
    const kids = children.get(id) ?? []
    return Math.max(size.get(id)!.width, kids.reduce((sum, c) => sum + span(c, next), 0) + Math.max(0, kids.length - 1) * 56)
  }
  const place = (id: string, x: number, y: number, seen = new Set<string>()) => {
    if (seen.has(id)) return
    const next = new Set(seen).add(id)
    const width = span(id)
    positions.set(id, { x: x + (width - size.get(id)!.width) / 2, y })
    let cursor = x
    for (const c of children.get(id) ?? []) {
      place(c, cursor, y + size.get(id)!.height + 100, next)
      cursor += span(c) + 56
    }
  }
  const top = payload.nodes.filter(n => !members.has(n.id) && !parent.has(n.id))
  const connected = top.filter(n => children.has(n.id) || (n.kind === 'device' && n.device?.is_self))
  const unplaced = top.filter(n => !connected.includes(n))
  let x = 0
  for (const n of connected.sort((a, b) => Number(!!b.device?.is_self) - Number(!!a.device?.is_self) || a.id.localeCompare(b.id))) {
    place(n.id, x, 0)
    x += span(n.id) + 100
  }
  const bottom = Math.max(0, ...[...positions].map(([id, p]) => p.y + size.get(id)!.height))
  for (let i = 0; i < unplaced.length; i++) {
    positions.set(unplaced[i].id, { x: (i % 4) * COLUMN, y: bottom + (connected.length ? 150 : 0) + Math.floor(i / 4) * ROW })
  }
  // Cycles and disconnected components remain visible without claiming a parent.
  for (const n of payload.nodes) if (!members.has(n.id) && !positions.has(n.id)) {
    positions.set(n.id, { x: (positions.size % 4) * COLUMN, y: bottom + 150 + Math.floor(positions.size / 4) * ROW })
  }
  const saved = new Map(payload.layout.map(p => [p.node_id, p]))
  for (const [id, p] of saved) if (positions.has(id)) positions.set(id, { x: p.x, y: p.y })
  if (uplink && !saved.get(uplink.node)?.pinned) {
    const local = positions.get(uplink.router)!
    const current = positions.get(uplink.node)!
    if (current.y >= local.y) positions.set(uplink.node, { x: local.x, y: local.y - 176 })
  }
  for (const [groupId, ids] of groups) {
    const group = { ...positions.get(groupId)! }
    for (const id of ids) {
      const p = saved.get(id)
      if (p) { group.x = Math.min(group.x, p.x - 24); group.y = Math.min(group.y, p.y - 78) }
    }
    positions.set(groupId, group)
    const cols = Math.min(4, ids.length)
    ids.forEach((id, i) => {
      const p = saved.get(id)
      positions.set(id, p ? { x: Math.max(16, p.x - group.x), y: Math.max(78, p.y - group.y) } : { x: 24 + (i % cols) * COLUMN, y: 78 + Math.floor(i / cols) * ROW })
    })
    const bounds = ids.map(id => positions.get(id)!)
    const current = size.get(groupId)!
    size.set(groupId, { width: Math.max(current.width, ...bounds.map(p => p.x + NODE_WIDTH + 24)), height: Math.max(current.height, ...bounds.map(p => p.y + 112)) })
  }
  return { parent, groups, positions, size, unplaced: unplaced.map(n => n.id), byId, uplink }
}

/** Port identity is resolved from interface data or a human label, never an ID. */
export function edgePort(payload: Topology, edge: Topology['edges'][number]): string | undefined {
  const iface = payload.nodes.find(n => n.interface?.id === edge.source_interface_id)?.interface
  return iface?.name || edge.label || undefined
}

export function segmentPort(payload: Topology, segment: TopologyNode): string | undefined {
  const ids = new Set(segment.children ?? [])
  const edge = payload.edges.find(e => ids.has(e.target_id))
  return edge ? edgePort(payload, edge) : undefined
}
