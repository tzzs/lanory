import { describe, expect, it } from 'vitest'
import { buildFlow, hideOffline, iconKeyForDeviceType, isDetailZoom, DETAIL_ZOOM } from '@/features/topology/graph'
import type { Topology, TopologyNode, DeviceType } from '@/types/api'

const layout: Topology['layout'] = []
const generated = new Date().toISOString()

function deviceNode(id: string, name: string, type: DeviceType, ip?: string): TopologyNode {
  const now = new Date().toISOString()
  return {
    id,
    kind: 'device' as const,
    device: {
      id,
      name,
      hostname: '',
      type,
      manufacturer: '',
      model: '',
      description: '',
      is_manual: false,
      is_locked: false,
      is_hidden: false,
      state: 'online',
      last_state_change: now,
      discovery_sources: ['bridge_fdb'],
      is_gateway: false,
      is_self: false,
      first_seen: now,
      last_seen: now,
      created_at: now,
      updated_at: now,
    },
    state: 'online',
    // TopologyNode carries the primary address, per the API contract.
    primary_ip: ip,
  }
}

describe('iconKeyForDeviceType', () => {
  it('maps the types named in the spec', () => {
    expect(iconKeyForDeviceType('internet')).toBe('globe')
    expect(iconKeyForDeviceType('router')).toBe('router')
    expect(iconKeyForDeviceType('switch')).toBe('network')
    expect(iconKeyForDeviceType('wireless_ap')).toBe('wifi')
    expect(iconKeyForDeviceType('desktop')).toBe('monitor')
    expect(iconKeyForDeviceType('laptop')).toBe('laptop')
    expect(iconKeyForDeviceType('smartphone')).toBe('smartphone')
    expect(iconKeyForDeviceType('server')).toBe('server')
    expect(iconKeyForDeviceType('nas')).toBe('hard-drive')
  })

  it('falls back to a generic icon', () => {
    expect(iconKeyForDeviceType('camera')).toBe('generic')
    expect(iconKeyForDeviceType(undefined)).toBe('generic')
  })
})

describe('buildFlow', () => {
  it('produces one node per topology node', () => {
    const payload: Topology = {
      nodes: [deviceNode('dev-1', 'Router', 'router'), deviceNode('dev-2', 'PC', 'desktop', '192.0.2.116')],
      edges: [],
      layout,
      generated_at: generated,
    }
    const { nodes, edges } = buildFlow(payload, { unverifiedThreshold: 0.6 })
    expect(nodes).toHaveLength(2)
    expect(edges).toHaveLength(0)
    expect(nodes[1].data.primaryIp).toBe('192.0.2.116')
  })

  it('draws unverified edges dashed', () => {
    const payload: Topology = {
      nodes: [deviceNode('dev-1', 'Router', 'router'), deviceNode('dev-2', 'iPhone', 'smartphone')],
      edges: [
        {
          id: 'edge-1',
          source_id: 'dev-1',
          target_id: 'dev-2',
          edge_type: 'observed_mac_port',
          discovery_source: 'bridge_fdb',
          confidence: 0.4,
          user_confirmed: false,
          locked: false,
          status: 'unverified',
          unverified: true,
          evidence_count: 1,
          last_observed: generated,
          created_at: generated,
          updated_at: generated,
        },
      ],
      layout,
      generated_at: generated,
    }
    const { edges } = buildFlow(payload, { unverifiedThreshold: 0.6 })
    expect(edges).toHaveLength(1)
    expect(edges[0].style?.strokeDasharray).toBe('5 4')
    expect(edges[0].data?.unverified).toBe(true)
  })

  it('draws confirmed edges solid and thicker', () => {
    const payload: Topology = {
      nodes: [deviceNode('dev-1', 'Router', 'router'), deviceNode('dev-2', 'iPhone', 'smartphone')],
      edges: [
        {
          id: 'edge-1',
          source_id: 'dev-1',
          target_id: 'dev-2',
          edge_type: 'user_defined',
          discovery_source: 'manual',
          confidence: 1,
          user_confirmed: true,
          locked: true,
          status: 'link_up',
          unverified: false,
          evidence_count: 0,
          last_observed: generated,
          created_at: generated,
          updated_at: generated,
        },
      ],
      layout,
      generated_at: generated,
    }
    const { edges } = buildFlow(payload, { unverifiedThreshold: 0.6 })
    expect(edges[0].style?.strokeDasharray).toBeUndefined()
    expect(edges[0].style?.strokeWidth).toBe(2)
  })

  it('skips edges whose endpoints are missing', () => {
    const payload: Topology = {
      nodes: [deviceNode('dev-1', 'Router', 'router')],
      edges: [
        {
          id: 'edge-1',
          source_id: 'dev-1',
          target_id: 'missing',
          edge_type: 'physical_link',
          discovery_source: 'manual',
          confidence: 1,
          user_confirmed: true,
          locked: false,
          status: 'link_up',
          unverified: false,
          evidence_count: 0,
          last_observed: generated,
          created_at: generated,
          updated_at: generated,
        },
      ],
      layout,
      generated_at: generated,
    }
    const { edges } = buildFlow(payload, { unverifiedThreshold: 0.6 })
    expect(edges).toHaveLength(0)
  })

  it('uses persisted positions when available', () => {
    const payload: Topology = {
      nodes: [deviceNode('dev-1', 'Router', 'router')],
      edges: [],
      layout: [{ id: 'lay-1', node_id: 'dev-1', x: 123, y: 456, pinned: true, updated_at: generated }],
      generated_at: generated,
    }
    const { nodes } = buildFlow(payload, { unverifiedThreshold: 0.6 })
    expect(nodes[0].position).toEqual({ x: 123, y: 456 })
    expect(nodes[0].draggable).toBe(false)
  })

  it('marks unresolved segments as dashed nodes', () => {
    const payload: Topology = {
      nodes: [{ id: 'seg-1', kind: 'device', state: 'unknown', unresolved: true }],
      edges: [],
      layout,
      generated_at: generated,
    }
    const { nodes } = buildFlow(payload, { unverifiedThreshold: 0.6 })
    expect(nodes[0].data.kind).toBe('unresolved')
  })
})

it('groups shared-port clients beneath their unresolved segment without implying direct links', () => {
  const router = deviceNode('router', 'Router', 'router')
  router.device!.is_self = true
  router.children = ['segment']
  const segment: TopologyNode = { id: 'segment', kind: 'network', state: 'unknown', unresolved: true, children: ['phone', 'pc'], network: { id: 'segment', name: 'Unresolved segment on eth1', interface_id: '', role: 'lan', device_count: 2 } }
  const payload: Topology = { nodes: [deviceNode('pc', 'PC', 'desktop'), router, deviceNode('phone', 'Phone', 'smartphone'), segment], edges: ['phone', 'pc'].map((id) => ({ id: 'edge-'+id, source_id: 'router', target_id: id, source_interface_id: 'iface-router-eth1', label: 'eth1', edge_type: 'observed_mac_port', discovery_source: 'bridge_fdb', confidence: .95, user_confirmed: false, locked: false, status: 'link_up', unverified: false, evidence_count: 1, last_observed: generated, created_at: generated, updated_at: generated })), layout: [], generated_at: generated }
  const { nodes, edges } = buildFlow(payload, { unverifiedThreshold: .6 })
  const byId = new Map(nodes.map(n => [n.id, n]))
  expect(byId.get('router')!.position.y).toBeLessThan(byId.get('segment')!.position.y)
  expect(byId.get('phone')!.parentId).toBe('segment')
  expect(byId.get('pc')!.parentId).toBe('segment')
  expect(edges.some(e => e.source === 'router' && e.target === 'segment')).toBe(true)
  expect(edges.some(e => e.source === 'router' && e.target === 'phone')).toBe(false)
  expect(edges.some(e => String(e.label).includes('iface-router'))).toBe(false)
  expect(byId.get('segment')!.style?.width).toBeGreaterThan(400)
})

it('uses the actual device id for the inspector rather than the topology node id', () => {
  const node = deviceNode('dev-router', 'Router', 'router')
  node.device!.id = 'router'
  const { nodes } = buildFlow({ nodes: [node], edges: [], layout: [], generated_at: generated }, { unverifiedThreshold: .6 })
  expect(nodes[0].data.nodeId).toBe('router')
})

it('keeps logical networks out of the router hierarchy and preserves pinned device coordinates', () => {
  const root = deviceNode('router', 'Router', 'router'); root.device!.is_self = true; root.children = ['segment']
  const client = deviceNode('pc', 'PC', 'desktop')
  const network: TopologyNode = { ...deviceNode('network', 'Router', 'router'), kind: 'network', network: { id: 'net', name: 'br-lan', interface_id: '', role: 'lan', device_count: 1 } }
  network.device!.is_self = true
  const segment: TopologyNode = { id: 'segment', kind: 'network', state: 'unknown', unresolved: true, children: ['pc'] }
  const { nodes } = buildFlow({ nodes: [root, segment, client, network], edges: [], generated_at: generated, layout: [{ id: 'saved', node_id: 'pc', x: 50, y: 500, pinned: true, updated_at: generated }] }, { unverifiedThreshold: .6 })
  const group = nodes.find(n => n.id === 'segment')!, pc = nodes.find(n => n.id === 'pc')!
  expect(pc.position.x + group.position.x).toBe(50)
  expect(pc.position.y + group.position.y).toBe(500)
  expect(pc.draggable).toBe(false)
  expect(group.draggable).toBe(false)
  expect(nodes.find(n => n.id === 'network')!.position.y).toBeGreaterThan(group.position.y)
})

it('places the active WAN above the local router even when old saved positions put it below', () => {
  const router = deviceNode('router', 'Router', 'router'); router.device!.id = 'r'; router.device!.is_self = true
  const iface = { id: 'if-wan', device_id: 'r', name: 'eth3', role: 'wan', carrier: true, speed_mbps: 100 } as import('@/types/api').NetworkInterface
  const wan: TopologyNode = { id: 'net-eth3', kind: 'network', state: 'online', interface: iface, network: { id: 'wan', interface_id: iface.id, name: 'eth3', role: 'wan', cidr: '192.168.1.16/24', device_count: 0 } }
  const graph: Topology = { nodes: [router, wan], edges: [], generated_at: generated, layout: [{ id: 'r', node_id: router.id, x: 200, y: 0, pinned: false, updated_at: generated }, { id: 'w', node_id: wan.id, x: 0, y: 872, pinned: false, updated_at: generated }] }
  const { nodes, edges } = buildFlow(graph, { unverifiedThreshold: .6, interfaces: [iface] })
  expect(nodes.find(n => n.id === wan.id)!.position.y).toBeLessThan(nodes.find(n => n.id === router.id)!.position.y)
  expect(edges.some(e => e.source === wan.id && e.target === router.id)).toBe(true)
  expect(nodes.find(n => n.id === wan.id)!.data.label).toBe('WAN · eth3')
  expect(nodes.find(n => n.id === wan.id)!.data.subtitle).toContain('100 Mbps')
})

describe('hideOffline', () => {
  function stateful(id: string, state: string, opts: Partial<TopologyNode> = {}): TopologyNode {
    const node = deviceNode(id, id, 'desktop')
    node.state = state as TopologyNode['state']
    node.device!.state = state as TopologyNode['state']
    return { ...node, ...opts }
  }

  it('drops offline devices and their edges', () => {
    const payload: Topology = {
      nodes: [stateful('dev-1', 'online'), stateful('dev-2', 'offline')],
      edges: [
        {
          id: 'edge-1',
          source_id: 'dev-1',
          target_id: 'dev-2',
          edge_type: 'user_defined',
          discovery_source: 'manual',
          confidence: 1,
          user_confirmed: true,
          locked: false,
          status: 'link_up',
          unverified: false,
          evidence_count: 0,
          last_observed: generated,
          created_at: generated,
          updated_at: generated,
        },
      ],
      layout,
      generated_at: generated,
    }
    const built = buildFlow(payload, { unverifiedThreshold: 0.6 })
    const result = hideOffline(built.nodes, built.edges)
    expect(result.nodes.map((n) => n.id)).toEqual(['dev-1'])
    expect(result.edges).toHaveLength(0)
    expect(result.hidden).toBe(1)
  })

  it('keeps an offline parent that still has a visible child', () => {
    // Hiding the router would orphan everything behind it and claim those
    // devices have no upstream.
    const router = deviceNode('dev-router', 'Router', 'router')
    router.device!.is_self = true
    router.children = ['dev-child']
    const payload: Topology = {
      nodes: [router, stateful('dev-child', 'online')],
      edges: [
        {
          id: 'edge-1',
          source_id: 'dev-router',
          target_id: 'dev-child',
          edge_type: 'observed_mac_port',
          discovery_source: 'bridge_fdb',
          confidence: 0.95,
          user_confirmed: true,
          locked: false,
          status: 'link_up',
          unverified: false,
          evidence_count: 1,
          last_observed: generated,
          created_at: generated,
          updated_at: generated,
        },
      ],
      layout,
      generated_at: generated,
    }
    const built = buildFlow(payload, { unverifiedThreshold: 0.6 })
    // Force the router offline after the build so the parent link is intact.
    const nodes = built.nodes.map((n) => (n.id === 'dev-router' ? { ...n, data: { ...n.data, state: 'offline' } } : n))
    const result = hideOffline(nodes, built.edges)
    expect(result.nodes.map((n) => n.id).sort()).toEqual(['dev-child', 'dev-router'])
    expect(result.hidden).toBe(0)
  })

  it('never drops network nodes', () => {
    const payload: Topology = {
      nodes: [
        stateful('dev-1', 'offline'),
        { id: 'net-br-lan', kind: 'network', state: 'unknown', network: { id: 'br-lan', name: 'br-lan', interface_id: 'if-1', role: 'lan', device_count: 0 } },
      ],
      edges: [],
      layout,
      generated_at: generated,
    }
    const built = buildFlow(payload, { unverifiedThreshold: 0.6 })
    const result = hideOffline(built.nodes, built.edges)
    expect(result.nodes.map((n) => n.id)).toEqual(['net-br-lan'])
  })
})

describe('zoom tier', () => {
  const payload: Topology = {
    nodes: [deviceNode('dev-1', 'Router', 'router'), deviceNode('dev-2', 'PC', 'desktop', '192.0.2.116')],
    edges: [],
    layout,
    generated_at: generated,
  }

  it('keeps the threshold inside the range where a card can still be read', () => {
    // 0 or 2 would silently disable the tier, so the constant is guarded.
    expect(DETAIL_ZOOM).toBeGreaterThan(0)
    expect(DETAIL_ZOOM).toBeLessThan(1)
  })

  it('flips the tier at the exported threshold', () => {
    expect(isDetailZoom(DETAIL_ZOOM)).toBe(true)
    expect(isDetailZoom(DETAIL_ZOOM - 0.01)).toBe(false)
  })

  it('marks the built nodes with the tier they were built for', () => {
    const detailed = buildFlow(payload, { unverifiedThreshold: 0.6 })
    expect(detailed.nodes.every(n => n.data.showDetail === true)).toBe(true)

    const low = buildFlow(payload, { unverifiedThreshold: 0.6, showDetail: false })
    expect(low.nodes.every(n => n.data.showDetail === false)).toBe(true)
  })

  it('defaults to the detailed tier when no tier is given', () => {
    const { nodes } = buildFlow(payload, { unverifiedThreshold: 0.6 })
    expect(nodes[0].data.showDetail).toBe(true)
  })
})
