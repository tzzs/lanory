/**
 * Minimal i18n layer.
 *
 * Lanory ships Chinese and English. Keys are dotted paths; interpolation uses
 * `{name}` placeholders.
 *
 * Ownership of the language value lives in one place: the persisted
 * `useUIStore` (`web/src/stores/ui.ts`). This module is a *subscriber* of that
 * store, not a parallel source of truth:
 *
 *   detectLanguage()  -> the only code that reads localStorage / navigator
 *   setLanguage()     -> writes the module value + localStorage + <html lang>
 *   useUIStore        -> persists the value and calls setLanguage() on change
 *
 * `t()` stays a plain function (most components `import { t } from '@/i18n'`),
 * so changing `current` alone does not re-render anything. Re-rendering is the
 * store's job: `AppShell` subscribes to `useUIStore((s) => s.language)` and keys
 * the routed subtree on it. Without that, clicking a language in Settings wrote
 * localStorage but left the visible text in the previous language until reload.
 */

export type Language = 'zh-CN' | 'en'

export const LANGUAGES: { value: Language; label: string; native: string }[] = [
  { value: 'zh-CN', label: 'Chinese', native: '简体中文' },
  { value: 'en', label: 'English', native: 'English' },
]

const STORAGE_KEY = 'lanory.language'

type Dict = Record<string, string>

const zhCN: Dict = {
  // ---- app shell ----
  'app.name': 'Lanory Network · 网绘',
  'app.slogan': '家的网络，一图看清。',
  'app.slogan_en': 'See your home network at a glance.',
  'app.loading': '加载中…',
  'app.error': '出错了',
  'app.retry': '重试',
  'app.render_error_body': '此页面渲染失败，其他页面仍可正常使用。',
  'app.cancel': '取消',
  'app.save': '保存',
  'app.delete': '删除',
  'app.edit': '编辑',
  'app.close': '关闭',
  'app.confirm': '确认',
  'app.search': '搜索',
  'app.filter': '筛选',
  'app.all': '全部',
  'app.none': '无',
  'app.unknown': '未知',
  'app.yes': '是',
  'app.no': '否',
  'app.copy': '复制',
  'app.copied': '已复制',
  'app.refresh': '刷新',
  'app.demo_banner': '演示模式：以下为模拟数据，并非真实扫描结果。',
  'app.demo_badge': '演示数据',
  'app.session_unavailable_title': '无法连接 iStoreOS 会话 / LuCI session unavailable',
  'app.session_unavailable_body': '请刷新 iStoreOS 页面或重新登录。 / Reload iStoreOS or sign in again.',
  'app.session_reload': '重试 / Retry',

  // ---- navigation ----
  'token.title': '需要访问令牌',
  'token.subtitle': 'Lanory 的 API 需要认证，请输入访问令牌',
  'token.label': '访问令牌',
  'token.description': '用于访问 Lanory API 的认证令牌',
  'token.placeholder': '粘贴 /etc/lanory/token 的内容',
  'token.submit': '进入',
  'token.empty': '请输入访问令牌',
  'token.clear': '清除已保存的令牌并重新输入',
  'token.how_title': '如何获取令牌？',
  'token.how_body': '令牌在首次安装时自动生成，存放在路由器上：',
  'token.how_command': 'ssh root@<路由器地址> \'cat /etc/lanory/token\'',
  'token.how_note': '令牌只会在系统日志中显示一次。如果忘记了，删除该文件后重启服务即可重新生成。',

  'nav.overview': '总览',
  'nav.topology': '网络拓扑',
  'nav.devices': '设备',
  'nav.monitoring': '网络监控',
  'nav.events': '事件记录',
  'nav.settings': '设置',
  'nav.section_main': '主要页面',
  'nav.section_system': '系统',

  // ---- overview ----
  'overview.title': '总览',
  'overview.subtitle': '家庭网络状态一览',
  'overview.total_devices': '总设备数',
  'overview.online': '在线设备',
  'overview.offline': '离线设备',
  'overview.unknown': '未知设备',
  'overview.interfaces': '网络接口',
  'overview.connection': '网络连接状态',
  'overview.recent_new': '最近新增设备',
  'overview.recent_offline': '最近离线设备',
  'overview.recent_events': '网络异常事件',
  'overview.lan_wan': 'LAN / WAN 状态',
  'overview.last_scan': '最近扫描',
  'overview.no_devices': '暂无设备',
  'overview.no_events': '暂无异常事件',
  'overview.healthy': '正常',
  'overview.degraded': '部分异常',
  'overview.down': '中断',
  'overview.view_all': '查看全部',
  'overview.never_scanned': '尚未扫描',
  'interface.up': '已连接',
  'interface.down': '已断开',
  'interface.no_carrier': '无载波',
  'interface.admin_up': '已启用',
  'interface.admin_down': '已禁用',

  // ---- topology ----
  'topology.title': '网络拓扑',
  'topology.subtitle': '自动发现的网络连接关系',
  'topology.auto_layout': '自动布局',
  'topology.manual_layout': '手动布局',
  'topology.fit_view': '适应视图',
  'topology.add_device': '添加设备',
  'topology.add_link': '添加连接',
  'topology.reset_layout': '重置布局',
  'topology.empty': '暂无拓扑数据，请先执行一次扫描。',
  'topology.node_details': '设备详情',
  'topology.edge_details': '连接详情',
  'topology.confidence': '可信度',
  'topology.evidence': '证据',
  'topology.unverified': '待确认',
  'topology.confirmed': '已确认',
  'topology.locked': '已锁定',
  'topology.lock': '锁定',
  'topology.unlock': '解锁',
  'topology.delete_edge': '删除连接',
  'topology.delete_edge_confirm': '确定要删除这条连接吗？',
  'topology.locked_edge_hint': '该连接已被用户确认，不会被自动探测覆盖。',
  'topology.select_node': '请选择一个节点',
  'topology.select_edge': '请选择一条连接',
  'topology.unresolved_segment': '下游网段',
  'topology.segment_hint': '同一端口后发现的设备，中间设备尚未识别',
  'topology.hierarchy_hint': '虚线表示观测或推断关系；网段分组不代表已确认的交换机或 AP。',
  'topology.unplaced': '其他网段与待定位设备',
  'topology.port_distribution': '端口与下游分布',
  'topology.unmapped_count': '{count} 台设备尚未定位',
  'topology.view_unmapped': '查看 {count} 台待定位设备',
  'topology.clients': '{count} 台设备',
  'topology.unknown_switch': '未知交换机 / AP',
  'topology.inferred_hint': '该关系为推断结果，缺乏直接证据。',
  'topology.layout_saved': '布局已保存',
  'topology.layout_tree': '树形布局',
  'topology.layout_layered': '分层布局',
  'topology.layout_force': '力导向布局',
  'topology.hide_offline': '隐藏离线',
  'topology.show_offline': '显示离线',
  'topology.offline_hidden': '已隐藏 {count} 台离线设备',
  'topology.offline_hidden_show': '显示',
  'topology.multi_homed': '多网段',
  'topology.multi_homed_hint': '该设备在多个网段上都有地址（例如 LAN 侧和 WAN 侧）',
  'topology.upstream': '上游',
  'topology.downstream': '下游',
  'topology.zoom_for_detail': '放大查看设备详情',

  // ---- interface roles ----
  'role.lan': 'LAN',
  'role.wan': 'WAN',
  'role.dmz': 'DMZ',
  'role.loopback': 'LOOPBACK',
  'role.virtual': 'VIRTUAL',
  'role.unknown': '未知',

  // ---- devices ----
  'devices.title': '设备',
  'devices.subtitle': '网络中已发现的所有设备',
  'devices.search_placeholder': '搜索名称、IP、MAC…',
  'devices.filter_type': '设备类型',
  'devices.filter_state': '在线状态',
  'devices.filter_source': '发现来源',
  'devices.filter_interface': '网络接口',
  'devices.filter_new': '仅新设备',
  'devices.sort_name': '名称',
  'devices.sort_ip': 'IP',
  'devices.sort_last_seen': '最近出现',
  'devices.sort_type': '类型',
  'devices.col_name': '名称',
  'devices.col_type': '类型',
  'devices.col_ip': 'IP',
  'devices.col_mac': 'MAC',
  'devices.col_interface': '接口',
  'devices.col_source': '发现来源',
  'devices.col_status': '状态',
  'devices.col_last_seen': '最近出现',
  'devices.empty': '没有符合条件的设备',
  'devices.count': '共 {count} 台设备',
  'devices.add': '添加设备',
  'devices.merge': '合并设备',
  'devices.merge_hint': '将重复发现的设备合并为一个实体。',
  'devices.merge_source': '要合并的设备',
  'devices.merge_target': '合并到',
  'devices.hide': '隐藏',
  'devices.unhide': '取消隐藏',
  'devices.delete': '删除',
  'devices.delete_hide_hint': '隐藏后下次扫描不会重新创建该设备。',
  'devices.delete_manual_hint': '手动添加的设备可以被彻底删除。',
  'devices.manual_badge': '手动',
  'devices.hidden_badge': '已隐藏',
  'devices.possible_duplicate': '可能重复',

  // ---- device detail ----
  'device.name': '名称',
  'device.type': '设备类型',
  'device.ip': 'IP 地址',
  'device.mac': 'MAC 地址',
  'device.interface': '关联接口',
  'device.link_speed': '链路速率',
  'device.reachability': '可达性',
  'device.last_seen': '最近出现',
  'device.online_time': '在线时间',
  'device.first_seen': '首次发现',
  'device.last_check': '最近检测结果',
  'device.manufacturer': '厂商',
  'device.model': '型号',
  'device.hostname': '主机名',
  'device.description': '备注',
  'device.no_address': '暂无地址',
  'device.show_all_addresses': '显示全部 {count} 个地址',
  'device.show_fewer_addresses': '收起',
  'device.open_details': '查看设备详情',
  'device.sources': '发现来源',
  'device.addresses': '地址列表',
  'device.identities': '身份标识',
  'device.probes': '探测目标',
  'device.history': '历史记录',
  'device.edges': '连接关系',
  'device.no_probe': '尚无探测结果',
  'device.state_reason': '判定原因',
  'device.check_method': '检测方式',
  'device.check_source': '检测来源',

  // ---- monitoring ----
  'monitoring.title': '网络监控',
  'monitoring.subtitle': '设备连通性、端口状态与外网可达性',
  'monitoring.device_status': '设备在线状态',
  'monitoring.latency': '延迟',
  'monitoring.packet_loss': '丢包率',
  'monitoring.lan_wan': 'LAN / WAN 状态',
  'monitoring.port_speed': '网口速率',
  'monitoring.last_results': '最近检测结果',
  'monitoring.internet': 'Internet 可达状态',
  'monitoring.avg_latency': '平均延迟',
  'monitoring.max_latency': '最大延迟',
  'monitoring.min_latency': '最小延迟',
  'monitoring.no_probes': '尚未配置探测目标',
  'monitoring.run_now': '立即检测',
  'monitoring.add_probe': '添加探测目标',
  'monitoring.probe_name': '名称',
  'monitoring.probe_target': '目标',
  'monitoring.probe_protocol': '协议',
  'monitoring.probe_interval': '间隔（秒）',
  'monitoring.probe_enabled': '启用',
  'monitoring.probe_disabled': '已停用',
  'monitoring.source_note': '检测结果来自 Lanory 所在设备的视角，不代表其他终端之间的实际连通性。',
  'monitoring.port_state': '端口状态',
  'monitoring.rx': '接收',
  'monitoring.tx': '发送',
  'monitoring.errors': '错误',
  'monitoring.dropped': '丢弃',

  // ---- events ----
  'events.title': '事件记录',
  'events.subtitle': '网络变化历史',
  'events.filter_type': '事件类型',
  'events.filter_device': '设备',
  'events.filter_since': '起始时间',
  'events.col_time': '时间',
  'events.col_type': '类型',
  'events.col_device': '设备',
  'events.col_change': '变化',
  'events.col_message': '说明',
  'events.empty': '暂无事件',
  'events.total': '共 {count} 条',

  // ---- settings ----
  'settings.title': '设置',
  'settings.subtitle': 'Lanory 运行参数',
  'settings.appearance': '外观',
  'settings.theme': '主题',
  'settings.theme_light': '浅色',
  'settings.theme_dark': '深色',
  'settings.theme_system': '跟随系统',
  'settings.language': '语言',
  'settings.discovery': '网络发现',
  'settings.discovery_enabled': '启用自动发现',
  'settings.discovery_interval': '发现间隔',
  'settings.collectors': '采集器',
  'settings.monitoring': '监控',
  'settings.monitoring_enabled': '启用连通性监控',
  'settings.monitoring_interval': '检测间隔',
  'settings.failure_threshold': '离线判定阈值',
  'settings.topology': '拓扑',
  'settings.auto_layout': '自动布局',
  'settings.preserve_layout': '保留手动布局',
  'settings.security': '安全',
  'settings.auth_token': '访问令牌',
  'settings.auth_note': '生产部署请务必配置访问令牌，避免未授权访问。',
  'settings.database': '数据库',
  'settings.db_path': '数据库路径',
  'settings.db_size': '数据库大小',
  'settings.db_rows': '数据行数',
  'settings.db_schema': 'Schema 版本',
  'settings.retention': '数据保留天数',
  'settings.about': '关于',
  'settings.version': '版本',
  'settings.saved': '已保存',
  'settings.save_failed': '保存失败',
  'settings.readonly_note': 'Lanory 为只读工具，不会修改 WAN、LAN、DHCP、防火墙、DNS 或路由表。',

  // ---- states ----
  'state.online': '在线',
  'state.offline': '离线',
  'state.unknown': '未知',
  'state.sleeping': '休眠',

  // ---- device types ----
  'type.internet': 'Internet',
  'type.modem_ont': '光猫 / ONT',
  'type.router': '路由器',
  'type.firewall': '防火墙',
  'type.switch': '交换机',
  'type.wireless_ap': '无线 AP',
  'type.mesh_node': 'Mesh 节点',
  'type.nas': 'NAS',
  'type.server': '服务器',
  'type.desktop': '台式机',
  'type.laptop': '笔记本',
  'type.smartphone': '手机',
  'type.tablet': '平板',
  'type.smartwatch': '手表',
  'type.smart_tv': '智能电视',
  'type.iot': 'IoT 设备',
  'type.printer': '打印机',
  'type.camera': '摄像头',
  'type.vm': '虚拟机',
  'type.container': '容器',
  'type.network_fiber': '光纤',
  'type.generic_node': '通用节点',
  'type.unknown': '未知',

  // ---- edge types ----
  'edge.physical_link': '物理连接',
  'edge.bridge_membership': '网桥成员',
  'edge.observed_mac_port': '端口学习',
  'edge.wireless_assoc': '无线关联',
  'edge.logical_network': '逻辑网络',
  'edge.user_defined': '手动添加',

  // ---- sources ----
  'source.openwrt': 'OpenWrt',
  'source.uci': 'UCI',
  'source.ubus': 'ubus',
  'source.arp': 'ARP',
  'source.ndp': 'NDP',
  'source.dhcp': 'DHCP',
  'source.bridge_fdb': 'Bridge FDB',
  'source.lldp': 'LLDP',
  'source.mdns': 'mDNS',
  'source.ssdp': 'SSDP',
  'source.snmp': 'SNMP',
  'source.xiaomi': '小米适配器',
  'source.scan': '主动扫描',
  'source.manual': '手动添加',
  'source.platform': '平台',
  'source.demo': '演示',

  // ---- events types ----
  'event.device.online': '设备上线',
  'event.device.offline': '设备离线',
  'event.device.discovered': '发现新设备',
  'event.device.removed': '设备移除',
  'event.device.updated': '设备更新',
  'event.device.ip_changed': 'IP 变更',
  'event.device.mac_changed': 'MAC 变更',
  'event.link.up': '链路上线',
  'event.link.down': '链路断开',
  'event.speed.changed': '速率变化',
  'event.topology.changed': '拓扑变化',
  'event.probe.completed': '探测完成',
  'event.discovery.started': '开始扫描',
  'event.discovery.completed': '扫描完成',
  'event.device.merged': '设备合并',
  'event.device.hidden': '设备隐藏',

  // ---- reason codes ----
  'reason.probe.reachable': '从 {source} 通过 {protocol} 可达',
  'reason.probe.unreachable_consecutive': '从 {source} 通过 {protocol} 连续 {failures} 次探测无响应，近期没有成功记录',
  'reason.probe.unreachable_source_down': '从 {source} 通过 {protocol} 连续 {failures} 次探测无响应，但探测源本身失去连接，无法据此判断设备',
  'reason.probe.unreachable_still_present': '从 {source} 通过 {protocol} 连续 {failures} 次探测无响应，但邻居表仍列出 {address}；设备仍在网段上，只是不响应此探测方式',
  'reason.probe.unreachable_power_cycling': '从 {source} 通过 {protocol} 连续 {failures} 次探测无响应，但设备在之前 {gap} 次失败后恢复，可能处于省电状态',
  'reason.probe.unreachable_deeper_sleep': '从 {source} 通过 {protocol} 连续 {failures} 次探测无响应；设备已处于休眠状态，最近一次成功响应为 {last_success}',
  'reason.probe.intermittent': '从 {source} 通过 {protocol} 间歇性可达：最近 {total} 次探测中有 {failed} 次失败',
  'reason.probe.confirming_recovery': '正在从 {source} 通过 {protocol} 确认恢复：已成功 {successes} 次，共需 {required} 次',
  'reason.probe.below_threshold': '从 {source} 通过 {protocol} 连续失败 {failures} 次，低于 {threshold} 次阈值，证据不足以改变状态',
  'reason.probe.never_succeeded': '设备尚未从 {source} 通过 {protocol} 成功响应（失败 {failures} 次，阈值 {threshold} 次）',
  'reason.probe.never_probed': '设备尚未从 {source} 探测：暂无任何证据',
  'reason.link.carrier_up': '链路已连接（{operstate}，{speed_mbps} Mbps）',
  'reason.link.carrier_up_no_speed': '链路已连接（{operstate}）',
  'reason.link.carrier_down': '链路断开（{operstate}）',
  'reason.link.operstate_up_carrier_unknown': '系统状态为已启用，但物理载波未知',
  'reason.link.no_information': '没有可用的载波或系统状态信息',
  'reason.link.carrier_changed': '{interface} 载波状态发生变化',
  'reason.link.speed_changed': '{interface} 协商速率发生变化',
  'reason.probe.awaiting_first': '等待首次探测',
  'reason.device.awaiting_first_check': '等待首次可达性检查',
  'reason.device.not_yet_probed': '尚未探测',
  'reason.topology.unknown_intermediate': '中间设备未知',
  'reason.anomaly.no_carrier': '{interface} 没有物理载波；管理状态为 {admin_state}',
  'reason.anomaly.devices_offline': '{count} 台设备离线',
  'reason.anomaly.devices_unknown': '{count} 台设备状态未知',
  'reason.anomaly.no_carrier_detail': '管理状态为 {admin_state}，但物理链路已断开。',
  'reason.anomaly.devices_offline_detail': '这些设备连续多次可达性检查失败。',
  'reason.anomaly.devices_unknown_detail': '证据不足，无法判断这些设备是否可达。启用监控以收集证据。',
  'severity.info': '提示',
  'severity.warning': '警告',
  'severity.critical': '严重',

  // ---- discovery ----
  'discovery.scan': '执行扫描',
  'discovery.scanning': '扫描中…',
  'discovery.cancel': '取消扫描',
  'discovery.status': '发现状态',
  'discovery.last_run': '最近运行',
  'discovery.collectors': '采集器',
  'discovery.supported': '可用',
  'discovery.unsupported': '不可用',
  'discovery.enabled': '已启用',
  'discovery.progress': '进度',
  'discovery.devices_found': '发现设备',
  'discovery.interfaces_found': '发现接口',
  'discovery.edges_created': '建立连接',
  'discovery.trigger_manual': '手动',
  'discovery.trigger_scheduled': '定时',
  'discovery.trigger_startup': '启动',
}

const en: Dict = {
  'app.name': 'Lanory Network',
  'app.slogan': 'See your home network at a glance.',
  'app.slogan_en': 'See your home network at a glance.',
  'app.loading': 'Loading…',
  'app.error': 'Something went wrong',
  'app.retry': 'Retry',
  'app.render_error_body': 'This page failed to render. Other pages still work.',
  'app.cancel': 'Cancel',
  'app.save': 'Save',
  'app.delete': 'Delete',
  'app.edit': 'Edit',
  'app.close': 'Close',
  'app.confirm': 'Confirm',
  'app.search': 'Search',
  'app.filter': 'Filter',
  'app.all': 'All',
  'app.none': 'None',
  'app.unknown': 'Unknown',
  'app.yes': 'Yes',
  'app.no': 'No',
  'app.copy': 'Copy',
  'app.copied': 'Copied',
  'app.refresh': 'Refresh',
  'app.demo_banner': 'Demo mode: the data below is simulated, not a real scan.',
  'app.demo_badge': 'Demo data',

  'token.title': 'Access token required',
  'token.subtitle': 'The Lanory API requires authentication',
  'token.label': 'Access token',
  'token.description': 'Authentication token for accessing the Lanory API',
  'token.placeholder': 'Paste the contents of /etc/lanory/token',
  'token.submit': 'Continue',
  'token.empty': 'Enter the access token',
  'token.clear': 'Clear the saved token and enter it again',
  'token.how_title': 'Where do I find the token?',
  'token.how_body': 'It is generated on first install and stored on the router:',
  'token.how_command': "ssh root@<router> 'cat /etc/lanory/token'",
  'token.how_note': 'The token is logged only once. If you lost it, delete the file and restart the service to generate a new one.',
  'app.session_unavailable_title': 'LuCI session unavailable',
  'app.session_unavailable_body': 'Reload iStoreOS or sign in again.',
  'app.session_reload': 'Retry',

  'nav.overview': 'Overview',
  'nav.topology': 'Topology',
  'nav.devices': 'Devices',
  'nav.monitoring': 'Monitoring',
  'nav.events': 'Events',
  'nav.settings': 'Settings',
  'nav.section_main': 'Main',
  'nav.section_system': 'System',

  'overview.title': 'Overview',
  'overview.subtitle': 'Your home network at a glance',
  'overview.total_devices': 'Total devices',
  'overview.online': 'Online',
  'overview.offline': 'Offline',
  'overview.unknown': 'Unknown',
  'overview.interfaces': 'Interfaces',
  'overview.connection': 'Connection',
  'overview.recent_new': 'Recently added',
  'overview.recent_offline': 'Recently offline',
  'overview.recent_events': 'Network anomalies',
  'overview.lan_wan': 'LAN / WAN status',
  'overview.last_scan': 'Last scan',
  'overview.no_devices': 'No devices yet',
  'overview.no_events': 'No anomalies',
  'overview.healthy': 'Healthy',
  'overview.degraded': 'Degraded',
  'overview.down': 'Down',
  'overview.view_all': 'View all',
  'overview.never_scanned': 'Never scanned',
  'interface.up': 'UP',
  'interface.down': 'DOWN',
  'interface.no_carrier': 'NO-CARRIER',
  'interface.admin_up': 'Enabled',
  'interface.admin_down': 'Disabled',

  'topology.title': 'Topology',
  'topology.subtitle': 'Discovered network relationships',
  'topology.auto_layout': 'Auto layout',
  'topology.manual_layout': 'Manual layout',
  'topology.fit_view': 'Fit view',
  'topology.add_device': 'Add device',
  'topology.add_link': 'Add link',
  'topology.reset_layout': 'Reset layout',
  'topology.empty': 'No topology yet. Run a scan to get started.',
  'topology.node_details': 'Device details',
  'topology.edge_details': 'Link details',
  'topology.confidence': 'Confidence',
  'topology.evidence': 'Evidence',
  'topology.unverified': 'Unverified',
  'topology.confirmed': 'Confirmed',
  'topology.locked': 'Locked',
  'topology.lock': 'Lock',
  'topology.unlock': 'Unlock',
  'topology.delete_edge': 'Delete link',
  'topology.delete_edge_confirm': 'Delete this link?',
  'topology.locked_edge_hint': 'This link was confirmed by you and will not be overwritten by automatic discovery.',
  'topology.select_node': 'Select a node',
  'topology.select_edge': 'Select a link',
  'topology.unresolved_segment': 'Downstream segment',
  'topology.segment_hint': 'Devices observed on one port; intermediate device unidentified',
  'topology.hierarchy_hint': 'Dashed lines show observed or inferred relations. A group does not confirm a switch or AP.',
  'topology.unplaced': 'Other networks and unplaced devices',
  'topology.port_distribution': 'Ports & downstream devices',
  'topology.unmapped_count': '{count} devices not yet placed',
  'topology.view_unmapped': 'View {count} unplaced devices',
  'topology.clients': '{count} devices',
  'topology.unknown_switch': 'Unknown switch / AP',
  'topology.inferred_hint': 'This relation is inferred and lacks direct evidence.',
  'topology.layout_saved': 'Layout saved',
  'topology.layout_tree': 'Tree layout',
  'topology.layout_layered': 'Layered layout',
  'topology.layout_force': 'Force layout',
  'topology.hide_offline': 'Hide offline',
  'topology.show_offline': 'Show offline',
  'topology.offline_hidden': '{count} offline devices hidden',
  'topology.offline_hidden_show': 'Show',
  'topology.multi_homed': 'Multi-subnet',
  'topology.multi_homed_hint': 'This device has addresses on more than one subnet (for example a LAN side and a WAN side)',
  'topology.upstream': 'Upstream',
  'topology.downstream': 'Downstream',
  'topology.zoom_for_detail': 'Zoom in for device details',

  // ---- interface roles ----
  'role.lan': 'LAN',
  'role.wan': 'WAN',
  'role.dmz': 'DMZ',
  'role.loopback': 'LOOPBACK',
  'role.virtual': 'VIRTUAL',
  'role.unknown': 'Unknown',

  'devices.title': 'Devices',
  'devices.subtitle': 'Everything discovered on your network',
  'devices.search_placeholder': 'Search name, IP, MAC…',
  'devices.filter_type': 'Device type',
  'devices.filter_state': 'Status',
  'devices.filter_source': 'Source',
  'devices.filter_interface': 'Interface',
  'devices.filter_new': 'New only',
  'devices.sort_name': 'Name',
  'devices.sort_ip': 'IP',
  'devices.sort_last_seen': 'Last seen',
  'devices.sort_type': 'Type',
  'devices.col_name': 'Name',
  'devices.col_type': 'Type',
  'devices.col_ip': 'IP',
  'devices.col_mac': 'MAC',
  'devices.col_interface': 'Interface',
  'devices.col_source': 'Source',
  'devices.col_status': 'Status',
  'devices.col_last_seen': 'Last seen',
  'devices.empty': 'No devices match your filters',
  'devices.count': '{count} devices',
  'devices.add': 'Add device',
  'devices.merge': 'Merge devices',
  'devices.merge_hint': 'Combine duplicate detections into a single entity.',
  'devices.merge_source': 'Device to merge',
  'devices.merge_target': 'Merge into',
  'devices.hide': 'Hide',
  'devices.unhide': 'Unhide',
  'devices.delete': 'Delete',
  'devices.delete_hide_hint': 'Hidden devices are not re-created by the next scan.',
  'devices.delete_manual_hint': 'Manually added devices can be deleted permanently.',
  'devices.manual_badge': 'Manual',
  'devices.hidden_badge': 'Hidden',
  'devices.possible_duplicate': 'Possible duplicate',

  'device.name': 'Name',
  'device.type': 'Type',
  'device.ip': 'IP address',
  'device.mac': 'MAC address',
  'device.interface': 'Interface',
  'device.link_speed': 'Link speed',
  'device.reachability': 'Reachability',
  'device.last_seen': 'Last seen',
  'device.online_time': 'Online for',
  'device.first_seen': 'First seen',
  'device.last_check': 'Last check',
  'device.manufacturer': 'Manufacturer',
  'device.model': 'Model',
  'device.hostname': 'Hostname',
  'device.description': 'Description',
  'device.no_address': 'No address',
  'device.show_all_addresses': 'Show all {count} addresses',
  'device.show_fewer_addresses': 'Show less',
  'device.open_details': 'View device details',
  'device.sources': 'Sources',
  'device.addresses': 'Addresses',
  'device.identities': 'Identities',
  'device.probes': 'Probes',
  'device.history': 'History',
  'device.edges': 'Links',
  'device.no_probe': 'No probe results yet',
  'device.state_reason': 'Reason',
  'device.check_method': 'Method',
  'device.check_source': 'Source',

  'monitoring.title': 'Monitoring',
  'monitoring.subtitle': 'Reachability, link health and internet connectivity',
  'monitoring.device_status': 'Device status',
  'monitoring.latency': 'Latency',
  'monitoring.packet_loss': 'Packet loss',
  'monitoring.lan_wan': 'LAN / WAN',
  'monitoring.port_speed': 'Port speed',
  'monitoring.last_results': 'Recent results',
  'monitoring.internet': 'Internet connectivity',
  'monitoring.avg_latency': 'Avg latency',
  'monitoring.max_latency': 'Max latency',
  'monitoring.min_latency': 'Min latency',
  'monitoring.no_probes': 'No probe targets configured',
  'monitoring.run_now': 'Run now',
  'monitoring.add_probe': 'Add probe',
  'monitoring.probe_name': 'Name',
  'monitoring.probe_target': 'Target',
  'monitoring.probe_protocol': 'Protocol',
  'monitoring.probe_interval': 'Interval (s)',
  'monitoring.probe_enabled': 'Enabled',
  'monitoring.probe_disabled': 'Disabled',
  'monitoring.source_note': 'Results are observed from the device running Lanory, not from other endpoints.',
  'monitoring.port_state': 'Port state',
  'monitoring.rx': 'RX',
  'monitoring.tx': 'TX',
  'monitoring.errors': 'Errors',
  'monitoring.dropped': 'Dropped',

  'events.title': 'Events',
  'events.subtitle': 'What changed on your network',
  'events.filter_type': 'Event type',
  'events.filter_device': 'Device',
  'events.filter_since': 'Since',
  'events.col_time': 'Time',
  'events.col_type': 'Type',
  'events.col_device': 'Device',
  'events.col_change': 'Change',
  'events.col_message': 'Detail',
  'events.empty': 'No events',
  'events.total': '{count} events',

  'settings.title': 'Settings',
  'settings.subtitle': 'Lanory runtime configuration',
  'settings.appearance': 'Appearance',
  'settings.theme': 'Theme',
  'settings.theme_light': 'Light',
  'settings.theme_dark': 'Dark',
  'settings.theme_system': 'System',
  'settings.language': 'Language',
  'settings.discovery': 'Discovery',
  'settings.discovery_enabled': 'Enable automatic discovery',
  'settings.discovery_interval': 'Discovery interval',
  'settings.collectors': 'Collectors',
  'settings.monitoring': 'Monitoring',
  'settings.monitoring_enabled': 'Enable connectivity monitoring',
  'settings.monitoring_interval': 'Probe interval',
  'settings.failure_threshold': 'Offline threshold',
  'settings.topology': 'Topology',
  'settings.auto_layout': 'Auto layout',
  'settings.preserve_layout': 'Preserve manual layout',
  'settings.security': 'Security',
  'settings.auth_token': 'Access token',
  'settings.auth_note': 'Always set an access token for production deployments.',
  'settings.database': 'Database',
  'settings.db_path': 'Path',
  'settings.db_size': 'Size',
  'settings.db_rows': 'Rows',
  'settings.db_schema': 'Schema version',
  'settings.retention': 'Retention (days)',
  'settings.about': 'About',
  'settings.version': 'Version',
  'settings.saved': 'Saved',
  'settings.save_failed': 'Save failed',
  'settings.readonly_note': 'Lanory is read-only: it never changes WAN, LAN, DHCP, firewall, DNS or routing.',

  'state.online': 'Online',
  'state.offline': 'Offline',
  'state.unknown': 'Unknown',
  'state.sleeping': 'Sleeping',

  'type.internet': 'Internet',
  'type.modem_ont': 'Modem / ONT',
  'type.router': 'Router',
  'type.firewall': 'Firewall',
  'type.switch': 'Switch',
  'type.wireless_ap': 'Wireless AP',
  'type.mesh_node': 'Mesh node',
  'type.nas': 'NAS',
  'type.server': 'Server',
  'type.desktop': 'Desktop',
  'type.laptop': 'Laptop',
  'type.smartphone': 'Smartphone',
  'type.tablet': 'Tablet',
  'type.smartwatch': 'Smartwatch',
  'type.smart_tv': 'Smart TV',
  'type.iot': 'IoT',
  'type.printer': 'Printer',
  'type.camera': 'Camera',
  'type.vm': 'VM',
  'type.container': 'Container',
  'type.network_fiber': 'Fiber',
  'type.generic_node': 'Generic node',
  'type.unknown': 'Unknown',

  'edge.physical_link': 'Physical link',
  'edge.bridge_membership': 'Bridge membership',
  'edge.observed_mac_port': 'Port learned',
  'edge.wireless_assoc': 'Wireless association',
  'edge.logical_network': 'Logical network',
  'edge.user_defined': 'Manual',

  'source.openwrt': 'OpenWrt',
  'source.uci': 'UCI',
  'source.ubus': 'ubus',
  'source.arp': 'ARP',
  'source.ndp': 'NDP',
  'source.dhcp': 'DHCP',
  'source.bridge_fdb': 'Bridge FDB',
  'source.lldp': 'LLDP',
  'source.mdns': 'mDNS',
  'source.ssdp': 'SSDP',
  'source.snmp': 'SNMP',
  'source.xiaomi': 'Xiaomi adapter',
  'source.scan': 'Active scan',
  'source.manual': 'Manual',
  'source.platform': 'Platform',
  'source.demo': 'Demo',

  'event.device.online': 'Device online',
  'event.device.offline': 'Device offline',
  'event.device.discovered': 'Device discovered',
  'event.device.removed': 'Device removed',
  'event.device.updated': 'Device updated',
  'event.device.ip_changed': 'IP changed',
  'event.device.mac_changed': 'MAC changed',
  'event.link.up': 'Link up',
  'event.link.down': 'Link down',
  'event.speed.changed': 'Speed changed',
  'event.topology.changed': 'Topology changed',
  'event.probe.completed': 'Probe completed',
  'event.discovery.started': 'Scan started',
  'event.discovery.completed': 'Scan completed',
  'event.device.merged': 'Device merged',
  'event.device.hidden': 'Device hidden',

  // ---- reason codes ----
  'reason.probe.reachable': 'Reachable from {source} via {protocol}',
  'reason.probe.unreachable_consecutive': 'Unreachable for {failures} consecutive probes from {source} via {protocol} with no recent success',
  'reason.probe.unreachable_source_down': 'Unreachable for {failures} consecutive probes from {source} via {protocol}, but the probe source lost connectivity',
  'reason.probe.unreachable_still_present': 'Unreachable for {failures} consecutive probes from {source} via {protocol}, but the neighbour table still lists {address}',
  'reason.probe.unreachable_power_cycling': 'Unreachable for {failures} consecutive probes from {source} via {protocol}, but the device resumed after an earlier gap of {gap} failures',
  'reason.probe.unreachable_deeper_sleep': 'Unreachable for {failures} consecutive probes from {source} via {protocol}; the device is sleeping and last answered {last_success}',
  'reason.probe.intermittent': 'Intermittent reachability from {source} via {protocol}: {failed} of the last {total} probes failed',
  'reason.probe.confirming_recovery': 'Confirming recovery from {source} via {protocol}: {successes} of {required} required successes',
  'reason.probe.below_threshold': '{failures} consecutive failures below the threshold of {threshold} from {source} via {protocol}',
  'reason.probe.never_succeeded': 'No successful probe yet from {source} via {protocol} ({failures} failures, threshold {threshold})',
  'reason.probe.never_probed': 'Never probed from {source}: no evidence either way',
  'reason.link.carrier_up': 'Carrier up ({operstate}, {speed_mbps} Mbps)',
  'reason.link.carrier_up_no_speed': 'Carrier up ({operstate})',
  'reason.link.carrier_down': 'Carrier down ({operstate})',
  'reason.link.operstate_up_carrier_unknown': 'Operstate is up but carrier is unknown',
  'reason.link.no_information': 'No carrier or operstate information available',
  'reason.link.carrier_changed': '{interface} carrier state changed',
  'reason.link.speed_changed': '{interface} negotiated speed changed',
  'reason.probe.awaiting_first': 'Awaiting first probe',
  'reason.device.awaiting_first_check': 'Waiting for the first reachability check',
  'reason.device.not_yet_probed': 'Not yet probed',
  'reason.topology.unknown_intermediate': 'Intermediate device is unknown',
  'reason.anomaly.no_carrier': '{interface} has no carrier; administrative state is {admin_state}',
  'reason.anomaly.devices_offline': '{count} device(s) offline',
  'reason.anomaly.devices_unknown': '{count} device(s) have unknown state',
  'reason.anomaly.no_carrier_detail': 'The interface is administratively {admin_state} but the physical link is down.',
  'reason.anomaly.devices_offline_detail': 'Devices that failed several consecutive reachability checks.',
  'reason.anomaly.devices_unknown_detail': 'Not enough evidence to say whether these devices are reachable. Enable monitoring to collect it.',
  'severity.info': 'Info',
  'severity.warning': 'Warning',
  'severity.critical': 'Critical',

  'discovery.scan': 'Run scan',
  'discovery.scanning': 'Scanning…',
  'discovery.cancel': 'Cancel scan',
  'discovery.status': 'Discovery status',
  'discovery.last_run': 'Last run',
  'discovery.collectors': 'Collectors',
  'discovery.supported': 'Supported',
  'discovery.unsupported': 'Unsupported',
  'discovery.enabled': 'Enabled',
  'discovery.progress': 'Progress',
  'discovery.devices_found': 'Devices found',
  'discovery.interfaces_found': 'Interfaces found',
  'discovery.edges_created': 'Links created',
  'discovery.trigger_manual': 'Manual',
  'discovery.trigger_scheduled': 'Scheduled',
  'discovery.trigger_startup': 'Startup',
}

const dictionaries: Record<Language, Dict> = { 'zh-CN': zhCN, en }

/**
 * The dictionaries, exposed so the contract test can compare them.
 *
 * A key that exists in one language but not the other is a bug the user sees
 * as one language of a screen staying in the other language.
 */
export const DICTIONARIES = dictionaries

let current: Language = detectLanguage()

/**
 * The one place that knows how to guess the language before the store exists:
 * an explicit stored choice wins, then the browser preference, then English
 * (which is also the dictionary fallback).
 */
export function detectLanguage(): Language {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (stored === 'zh-CN' || stored === 'en') return stored
  } catch {
    /* ignore */
  }
  if (typeof navigator !== 'undefined' && navigator.language?.toLowerCase().startsWith('zh')) {
    return 'zh-CN'
  }
  return 'en'
}

export function getLanguage(): Language {
  return current
}

/**
 * Applies a language: module value, localStorage and `<html lang>`.
 *
 * `<html lang>` matters beyond semantics: `lib/devices.ts:getLocale()` reads it
 * to pick the locale for `Intl` date/relative-time formatting, so a stale
 * attribute renders Chinese dates on an English page.
 *
 * Callers must invoke this once per change - `useUIStore.setLang` does the
 * writing and the store value is what components re-render on.
 */
export function setLanguage(lang: Language): void {
  current = lang
  try {
    localStorage.setItem(STORAGE_KEY, lang)
  } catch {
    /* ignore */
  }
  document.documentElement.lang = lang === 'zh-CN' ? 'zh-CN' : 'en'
}

/** Translates a key, interpolating `{name}` placeholders from params. */
export function translate(key: string, params?: Record<string, string | number>): string {
  // Read the dictionary on every call instead of closing over it: the language
  // can change while components are mounted (the store re-renders them).
  const dict = dictionaries[current] ?? en
  let value = dict[key]
  if (value === undefined) {
    // Fall back to English before giving up, so a missing Chinese string still
    // renders something meaningful.
    value = en[key]
  }
  if (value === undefined) return key
  if (params) {
    for (const [k, v] of Object.entries(params)) {
      value = value.replace(new RegExp(`\\{${k}\\}`, 'g'), String(v))
    }
  }
  return value
}

/** Bound translator for the current language. */
export function t(key: string, params?: Record<string, string | number>): string {
  return translate(key, params)
}

/**
 * State tokens the backend stores, and the key that names each one.
 *
 * The kernel reports an interface as "up" or "down" and the state machine
 * reports a device as "online" or "offline". Those tokens are what the
 * database stores, so the wording belongs here.
 */
const STATE_TOKENS: Record<string, string> = {
  up: 'interface.up',
  down: 'interface.down',
  online: 'state.online',
  offline: 'state.offline',
  sleeping: 'state.sleeping',
  unknown: 'state.unknown',
}

/**
 * Reason parameters whose value is an enum token rather than free text.
 *
 * The mapping is keyed by the parameter name, so a source id or an interface
 * name is never rewritten: only the parameters that are known to hold an enum
 * are looked up.
 */
const ENUM_PARAMS: Record<string, Record<string, string>> = {
  admin_state: {
    up: 'interface.admin_up',
    down: 'interface.admin_down',
    unknown: 'app.unknown',
  },
}

/**
 * Renders a state token ("up", "online") in the current language.
 *
 * A token that is not a known state is returned unchanged, so an IP address
 * or a hostname that happens to look like a state survives.
 */
export function stateTokenLabel(token: string): string {
  const key = STATE_TOKENS[token]
  if (!key) return token
  const label = translate(key)
  return label === key ? token : label
}

/**
 * Rewrites the enum parameters of a reason into the user's language.
 *
 * The backend sends the raw token because that is what it stores; the
 * frontend owns the wording.
 */
function localizeParams(params: Record<string, string>): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [name, value] of Object.entries(params)) {
    const key = ENUM_PARAMS[name]?.[value]
    if (!key) {
      out[name] = value
      continue
    }
    const label = translate(key)
    out[name] = label === key ? value : label
  }
  return out
}

/**
 * Renders a backend reason code in the current language.
 *
 * The backend still sends `fallback` for logs and old rows whose reason was
 * written before reason codes existed. Unknown codes also use that fallback;
 * a new server cannot make an older frontend render the wrong sentence.
 */
export function translateReason(
  reason: { code?: string; params?: Record<string, string> } | null | undefined,
  fallback = '',
): string {
  if (!reason?.code) return fallback
  const params = localizeParams(reason.params ?? {})
  const key = `reason.${reason.code}`
  const translated = translate(key, params)
  return translated === key ? fallback : translated
}

export const i18n = { t, translate, translateReason, stateTokenLabel, getLanguage, setLanguage, detectLanguage, languages: LANGUAGES }
