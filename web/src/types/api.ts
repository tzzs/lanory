/**
 * Lanory API types — TypeScript mirror of internal/model/model.go.
 *
 * This file is the frontend's half of the API contract. Keep it in sync with
 * the Go structs and docs/api.md. Field names must not change without bumping
 * the API version.
 */

// ---------------------------------------------------------------------------
// Enums
// ---------------------------------------------------------------------------

export type DeviceType =
  | 'internet'
  | 'modem_ont'
  | 'router'
  | 'firewall'
  | 'switch'
  | 'wireless_ap'
  | 'mesh_node'
  | 'nas'
  | 'server'
  | 'desktop'
  | 'laptop'
  | 'smartphone'
  | 'tablet'
  | 'smartwatch'
  | 'smart_tv'
  | 'iot'
  | 'printer'
  | 'camera'
  | 'vm'
  | 'container'
  | 'network_fiber'
  | 'generic_node'
  | 'unknown'

export type DeviceState = 'online' | 'offline' | 'unknown' | 'sleeping'

export type InterfaceRole = 'wan' | 'lan' | 'dmz' | 'loopback' | 'virtual' | 'unknown'
export type InterfaceType = 'ethernet' | 'bridge' | 'wireless' | 'vlan' | 'loopback' | 'virtual' | 'other'
export type AdminState = 'up' | 'down' | 'unknown'
export type OperState = 'up' | 'down' | 'unknown' | 'no-carrier'

export type EdgeType =
  | 'physical_link'
  | 'bridge_membership'
  | 'observed_mac_port'
  | 'wireless_assoc'
  | 'logical_network'
  | 'user_defined'

export type EdgeStatus = 'link_up' | 'link_down' | 'reachable' | 'unreachable' | 'unverified'

export type DiscoverySource =
  | 'openwrt' | 'uci' | 'ubus' | 'arp' | 'ndp' | 'dhcp' | 'bridge_fdb'
  | 'lldp' | 'mdns' | 'ssdp' | 'snmp' | 'xiaomi' | 'scan' | 'manual'
  | 'platform' | 'demo'

export type IdentityType = 'mac' | 'dhcp_client_id' | 'hostname' | 'vendor' | 'fingerprint'
export type AddressFamily = 'ipv4' | 'ipv6'

export type NeighborState =
  | 'reachable' | 'stale' | 'delay' | 'probe' | 'failed' | 'incomplete'
  | 'permanent' | 'none'

export type ProbeProtocol = 'icmp' | 'tcp' | 'http' | 'dns' | 'arp'
export type ProbeKind = 'device' | 'network'
export type ProbeRunStatus = 'success' | 'timeout' | 'refused' | 'error'

export type EventType =
  | 'device.online' | 'device.offline' | 'device.discovered' | 'device.removed'
  | 'device.updated' | 'device.ip_changed' | 'device.mac_changed'
  | 'link.up' | 'link.down' | 'speed.changed' | 'topology.changed'
  | 'probe.completed' | 'discovery.started' | 'discovery.completed'
  | 'device.merged' | 'device.hidden'

export type JobStatus = 'pending' | 'running' | 'completed' | 'failed' | 'cancelled'

// ---------------------------------------------------------------------------
// Entities
// ---------------------------------------------------------------------------

export interface Reason {
  code: string
  params?: Record<string, string>
}

export interface Device {
  id: string
  name: string
  hostname: string
  type: DeviceType
  manufacturer: string
  model: string
  description: string
  is_manual: boolean
  is_locked: boolean
  is_hidden: boolean
  type_source?: DiscoverySource
  state: DeviceState
  state_reason?: string
  state_reason_code?: Reason
  last_state_change: string
  primary_ipv4?: string
  primary_ipv6?: string
  primary_mac?: string
  /**
   * Sources that have observed the device. The API normalises this to `[]`,
   * but it is declared nullable because a nil Go slice marshals to `null` and
   * a client must survive that rather than take the page down.
   */
  discovery_sources: DiscoverySource[] | null
  is_gateway: boolean
  is_self: boolean
  first_seen: string
  last_seen: string
  created_at: string
  updated_at: string
}

export interface DeviceAddress {
  id: string
  device_id: string
  address: string
  family: AddressFamily
  interface_id?: string
  source: DiscoverySource
  prefix_length?: number
  is_primary: boolean
  first_seen: string
  last_seen: string
}

export interface DeviceIdentity {
  id: string
  device_id: string
  type: IdentityType
  value: string
  source: DiscoverySource
  confidence: number
  first_seen: string
  last_seen: string
}

export interface NetworkInterface {
  id: string
  device_id: string
  name: string
  mac?: string
  type: InterfaceType
  role: InterfaceRole
  admin_state: AdminState
  oper_state: OperState
  carrier: boolean
  speed_mbps: number
  mtu: number
  bridge_id?: string
  vlan_id?: number
  is_physical: boolean
  rx_bytes: number
  tx_bytes: number
  rx_errors: number
  tx_errors: number
  rx_packets: number
  tx_packets: number
  rx_dropped: number
  tx_dropped: number
  addresses?: DeviceAddress[]
  last_speed_change?: string
  last_seen: string
}

export interface TopologyEdge {
  id: string
  source_id: string
  target_id: string
  source_interface_id?: string
  target_interface_id?: string
  edge_type: EdgeType
  discovery_source: DiscoverySource
  confidence: number
  user_confirmed: boolean
  locked: boolean
  status: EdgeStatus
  label?: string
  evidence_count: number
  unverified: boolean
  last_observed: string
  created_at: string
  updated_at: string
}

export interface DiscoveryEvidence {
  id: string
  collector: string
  subject_type: string
  subject_id: string
  evidence_type: string
  raw_data: string
  observed_at: string
}

export interface ProbeTarget {
  id: string
  name: string
  kind: ProbeKind
  target: string
  port?: number
  protocol: ProbeProtocol
  source_id: string
  interval: number
  timeout: number
  enabled: boolean
  device_id?: string
  failure_threshold: number
  recover_confirm: number
  state: DeviceState
  state_reason?: string
  state_reason_code?: Reason
  consecutive_failures: number
  consecutive_successes: number
  last_result?: ProbeRunStatus
  last_latency_ms?: number
  last_error?: string
  last_run_at?: string
  last_success_at?: string
  created_at: string
  updated_at: string
}

export interface ProbeResult {
  id: string
  probe_id: string
  timestamp: string
  success: boolean
  latency_ms: number
  status: ProbeRunStatus
  error_type?: string
  error_message?: string
}

export interface NetworkEvent {
  id: string
  event_type: EventType
  device_id?: string
  edge_id?: string
  old_value?: string
  new_value?: string
  message?: string
  reason_code?: Reason
  evidence?: string
  timestamp: string
}

export interface TopologyLayout {
  id: string
  node_id: string
  x: number
  y: number
  pinned: boolean
  updated_at: string
}

export interface AppSetting {
  key: string
  value: string
  updated_at: string
}

// ---------------------------------------------------------------------------
// Composed types
// ---------------------------------------------------------------------------

export interface DeviceDetail {
  device: Device
  addresses: DeviceAddress[]
  identities: DeviceIdentity[]
  interfaces: NetworkInterface[]
  edges: TopologyEdge[]
  probes: ProbeTarget[]
  last_probe: ProbeResult | null
  events: NetworkEvent[]
}

export interface DeviceCounts {
  total: number
  online: number
  offline: number
  unknown: number
  sleeping: number
  hidden: number
  manual: number
}

export interface InterfaceStatus {
  interface: NetworkInterface
  role: InterfaceRole
  status: EdgeStatus
  reason?: string
  reason_code?: Reason
}

export interface NetworkStatus {
  id: string
  name: string
  interface_id: string
  cidr?: string
  gateway_ip?: string
  role: InterfaceRole
  device_count: number
}

export interface Anomaly {
  id: string
  severity: 'info' | 'warning' | 'critical'
  title: string
  detail: string
  reason_code?: Reason
  detail_reason_code?: Reason
  since: string
}

export interface Overview {
  counts: DeviceCounts
  interfaces: InterfaceStatus[]
  networks: NetworkStatus[]
  recent_new_devices: Device[]
  recent_offline_devices: Device[]
  recent_events: NetworkEvent[]
  anomalies: Anomaly[]
  last_scan_at: string
  discovery_enabled: boolean
  demo_mode: boolean
  generated_at: string
}

export interface TopologyNode {
  id: string
  kind: 'device' | 'network'
  device?: Device
  interface?: NetworkInterface
  network?: NetworkStatus
  state: DeviceState
  state_reason?: string
  primary_ip?: string
  primary_mac?: string
  /** Every address of the node, tagged with its interface and side. */
  addresses?: NodeAddress[]
  children?: string[]
  unresolved?: boolean
}

/**
 * One address of a topology node.
 *
 * `side` is what makes a multi-homed node legible: a router's LAN address and
 * its WAN address are different networks on different sides of the same box.
 * `cidr` is the network the address belongs to, so addresses can be grouped.
 */
export interface NodeAddress {
  address: string
  family: AddressFamily
  prefix_length?: number
  cidr?: string
  interface?: string
  side: InterfaceRole
  is_primary?: boolean
}

export interface Topology {
  nodes: TopologyNode[]
  edges: TopologyEdge[]
  layout: TopologyLayout[]
  generated_at: string
}

export interface EdgeEvidence {
  edge: TopologyEdge
  evidence: DiscoveryEvidence[]
}

export interface DeviceEvidence {
  device: Device
  evidence: DiscoveryEvidence[]
  addresses: DeviceAddress[]
}

export interface TopologyEvidence {
  edges: EdgeEvidence[]
  unmapped_devices: DeviceEvidence[]
}

export interface CollectorStatus {
  name: string
  supported: boolean
  capabilities: string[]
  description?: string
  enabled: boolean
  last_run_at?: string
  last_error?: string
  last_duration_ms?: number
  records?: number
}

export interface CollectorRun {
  name: string
  status: JobStatus
  duration_ms: number
  records: number
  error?: string
}

export interface DiscoveryJob {
  id: string
  status: JobStatus
  trigger: string
  started_at: string
  finished_at?: string
  progress: number
  collectors: CollectorRun[]
  error?: string
  devices_found: number
  devices_new: number
  interfaces_found: number
  edges_created: number
}

export interface DiscoveryStatus {
  running: boolean
  current_job_id?: string
  last_job?: DiscoveryJob
  collectors: CollectorStatus[]
  interval: string
  enabled: boolean
}

export interface ProbeStatus {
  probe: ProbeTarget
  /**
   * Recent results, newest first. The API normalises this to `[]` for a probe
   * that has never run, but it is declared nullable because a nil Go slice
   * marshals to `null` and any client must survive that rather than take the
   * whole page down.
   */
  recent: ProbeResult[] | null
  loss_pct: number
  avg_latency_ms: number
}

export interface MonitoringStatus {
  probes: ProbeStatus[]
  interfaces: InterfaceStatus[]
  generated_at: string
}

export interface EventList {
  events: NetworkEvent[]
  total: number
  limit: number
  offset: number
}

export interface DeviceList {
  devices: Device[]
  total: number
  limit: number
  offset: number
}

export interface ErrorResponse {
  error: string
  code?: string
  details?: Record<string, string>
}

export interface HealthResponse {
  status: string
  version: string
  demo_mode: boolean
  uptime_seconds: number
}

// ---------------------------------------------------------------------------
// Request bodies
// ---------------------------------------------------------------------------

export interface CreateDeviceRequest {
  name: string
  type: DeviceType
  hostname?: string
  ipv4?: string
  ipv6?: string
  mac?: string
  manufacturer?: string
  model?: string
  description?: string
  parent_device_id?: string
  connection_type?: EdgeType
  parent_interface_id?: string
}

export interface PatchDeviceRequest {
  name?: string
  type?: DeviceType
  hostname?: string
  manufacturer?: string
  model?: string
  description?: string
  is_locked?: boolean
  is_hidden?: boolean
}

export interface MergeDeviceRequest {
  source_device_id: string
}

export interface CreateEdgeRequest {
  source_id: string
  target_id: string
  edge_type?: EdgeType
  source_interface_id?: string
  target_interface_id?: string
  label?: string
}

export interface PatchEdgeRequest {
  edge_type?: EdgeType
  confidence?: number
  user_confirmed?: boolean
  locked?: boolean
  status?: EdgeStatus
  label?: string
  source_interface_id?: string
  target_interface_id?: string
}

export interface LayoutNode {
  node_id: string
  x: number
  y: number
  pinned?: boolean
}

export interface SaveLayoutRequest {
  nodes: LayoutNode[]
}

export interface AutoLayoutRequest {
  algorithm?: 'tree' | 'layered' | 'force'
}

export interface ScanRequest {
  collectors?: string[]
  subnets?: string[]
}

export interface CreateProbeRequest {
  name: string
  kind?: ProbeKind
  target?: string
  device_id?: string
  protocol?: ProbeProtocol
  port?: number
  interval?: number
  timeout?: number
  failure_threshold?: number
}

export interface PatchProbeRequest {
  name?: string
  target?: string
  protocol?: ProbeProtocol
  port?: number
  interval?: number
  timeout?: number
  enabled?: boolean
  failure_threshold?: number
}

export interface PatchSettingsRequest {
  [key: string]: string | number | boolean
}
