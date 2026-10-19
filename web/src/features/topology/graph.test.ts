import { describe, expect, it } from 'vitest'
import { buildFlow, iconKeyForDeviceType } from '@/features/topology/graph'
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
