# LAN 设备发现与类型识别：一手来源研究

日期：2026-11-03

## 摘要

开源网络工具通常把“发现”拆成数个不同任务：收集地址或 MAC、判断某个目标是否响应、识别支持管理协议的网络设备、导入 DHCP/控制器数据，以及建立人工维护的资产目录。没有一种通用的被动信号能从 IP/MAC 确定任意终端的真实设备类型，也无法仅凭一个端口上学到多个 MAC 证明端口后面有 AP 或交换机。

对 Lanory 最合适的方向是继续以路由器已有状态为主：保留 DHCP、邻居表、FDB、接口/桥信息和 LLDP；将主机名关键字从直接分类降为低置信度提示；在有条件时增加明确的协议/服务特征及可配置的 SNMP/控制器来源；让用户确认覆盖推断。主动扫描保持显式 opt-in，且只能解释为从扫描源观察到的响应。Xiaomi adapter 目前仅有 fake-device 测试、未在真实硬件验证，应继续作为未验证能力呈现。

## 术语：三个不同问题

| 问题 | 典型证据 | 能支持的结论 | 不能直接推出 |
| --- | --- | --- | --- |
| 身份/存在 | DHCP 租约、ARP/NDP 邻居、FDB MAC | 某 MAC/IP 曾被路由器分配、解析或在桥端口学习 | 设备当前在线、精确型号、Wi-Fi 状态 |
| 类型/平台 | SNMP `sysObjectID`/`sysDescr`、设备自报、服务公告、用户输入 | 满足规则的类型或平台候选 | 未支持管理协议的终端类型 |
| 拓扑/链路 | LLDP 邻居、桥 FDB、接口配置 | 广告邻居在该端口、MAC 在该端口被学习、接口属于桥 | 隐藏的中间盒类型、流量路径、多个 MAC 的共同物理设备 |

ARP 把协议地址映射为本地链路地址（[RFC 826](https://www.rfc-editor.org/rfc/rfc826)）；DHCP 为客户端配置地址及相关参数（[RFC 2131](https://www.rfc-editor.org/rfc/rfc2131)）。这两种机制本身并不定义终端设备分类。LLDP 是邻居在链路上发布系统/端口等信息的协议（[IEEE 802.1AB 标准页](https://standards.ieee.org/ieee/802.1AB/6844/)），只有对端发送并且采集点可见时才有相应证据。

## 项目比较

### NetAlertX：多来源家庭网络资产发现

NetAlertX README 列出 `arp-scan`、DHCP 租约导入、Pi-hole 数据、UniFi 控制器和 SNMP 路由器等来源，并通过插件机制扩展扫描器（[官方 README](https://github.com/netalertx/NetAlertX/blob/main/README.md)、[插件概览](https://docs.netalertx.com/PLUGINS_OVERVIEW)）。其远端网络文档说明 ARP 广播扫描主要覆盖同一二层广播域，路由网络、VLAN、VPN 等需要其他数据源或网络侧部署（[Remote Networks](https://docs.netalertx.com/REMOTE_NETWORKS/)）。

**可借鉴：** 按来源插拔采集、把 DHCP/控制器等已有数据用于补充扫描器、为远端网段说明采集盲区。`arp-scan` 结果仍然只是扫描时本地链路上可见的 IP/MAC 观察，不自动等于在线状态或精确类型。

### Nmap：主动主机发现与服务/操作系统探测工具

Nmap 主机发现支持 ICMP、TCP、UDP、SCTP、IP 协议探测；在本地以太网，默认 IPv4 通常使用 ARP、IPv6 使用 Neighbor Discovery，即使指定其他主机发现探针也是如此（[Nmap Host Discovery 手册](https://nmap.org/book/man-host-discovery.html)）。Nmap 的 OS detection 是另一个阶段，依赖对目标网络栈的探测响应与指纹数据库（[Nmap OS Detection 手册](https://nmap.org/book/osdetect.html)）。

**可借鉴：** 将“主机发现”与“OS/类型识别”作为不同结果；把探测方法、执行时间和扫描源记录下来。**边界：** 这是主动网络探测能力，结果受防火墙、目标响应和扫描位置影响；不能默认在 Lanory 用户网络中运行，也不能用一次响应替代持续状态。需要用户显式启用和指定授权范围。

### LibreNMS：管理协议设备监控与规则式 OS 识别

LibreNMS 的发现围绕已添加、可管理设备的 SNMP 数据。其 OS 检测文档推荐根据 `sysObjectID` 前缀匹配，`sysDescr` 可作补充；规则条件可组合，额外 `snmpget` 也有查询成本（[LibreNMS Initial Detection](https://docs.librenms.org/Developing/os/Initial-Detection/)、[Discovery Support](https://docs.librenms.org/Support/Discovery%20Support/)）。

**可借鉴：** 面向确实启用了 SNMP 的路由器/交换机，基于结构化且设备自报的字段建精确规则，规则来源和匹配项可解释。**边界：** 家用终端通常不开放 SNMP；`sysObjectID` 识别的是可管理设备的系统/平台，不能当成通用终端识别方案，也不能无凭据尝试管理接口。

### NetBox：人工维护的基础设施事实库

NetBox 将 Device Type（厂商/型号的可复用定义）与设备实例区分，类型可描述硬件组件；安装设备实例从类型生成初始组件（[NetBox Device Type 文档](https://netbox.readthedocs.io/en/stable/models/dcim/devicetype/)、[Device 模型](https://netbox.readthedocs.io/en/stable/models/dcim/device/)）。NetBox 的核心文档将其定位为基础设施 source of truth，而非通过被动 LAN 观察自动识别所有终端（[NetBox 文档](https://github.com/netbox-community/netbox/blob/main/docs/index.md)）。

**可借鉴：** 把“设备类别（router/AP/phone 等）”“厂商/型号”和“软件平台”分开，用户确认的数据是权威记录；发现结果可作为待确认建议。**边界：** 资产库模型不是识别算法，NetBox Labs 的 Discovery 是独立产品，不能把其能力当成 NetBox 核心功能。

### OpenWrt：路由器本地的一手接口与无线状态

OpenWrt `ubus` 文档提供网络接口状态查询，例如 `network.interface` 的 `dump`（[ubus 技术参考](https://openwrt.org/docs/techref/ubus)）。OpenWrt 无线工具文档记录 `iwinfo <device> assoclist`、`iw dev <device> station dump` 等查询关联客户端的方式（[iwinfo ubus 开发文档](https://openwrt.org/docs/guide-developer/ubus/iwinfo)、[无线工具参考](https://openwrt.org/docs/guide-user/network/wifi/wireless-tool/wireless.utilities)）。

**可借鉴：** 在本机 OpenWrt 上，从接口配置识别本地软件路由器及其 LAN/WAN 侧；在驱动/平台支持时读取关联表以确认无线关联。**边界：** 关联表只能描述该 AP/无线接口看到的关联客户端；路由器无法由自身接口配置证明另一台桥接设备就是 AP。支持度受驱动、hostapd、平台版本和权限影响，应检测 capability 与实际采集状态。

### `arp-scan`：同链路主动 ARP 扫描器

`arp-scan` 项目的官方手册定义其为使用 ARP 在本地网络发现 IPv4 主机的工具，包含 MAC 厂商查找等信息（[官方项目与手册](https://github.com/royhills/arp-scan)）。

**可借鉴：** 作为用户启用后的补充 IPv4 地址/MAC 观察来源；MAC 厂商前缀可显示为厂商提示。**边界：** ARP 扫描不跨路由器，也不会直接给出设备型号；厂商 OUI 标识 MAC 地址块注册方，不必然等于终端品牌（例如网卡模块/虚拟接口/随机化 MAC）。该工具会发包，不应作为无提示的默认后台行为。

## 对 Lanory 的可执行建议

### 1. 弱化 hostname 关键字分类，保留它作为提示

当前 `typeHint` 依赖 hostname 关键词。主机名可由客户端自行选择、重复、被 DHCP 提供或更改；它适合作为“可能是打印机/电视”的低置信度线索，不应被解释为型号事实。建议类型识别结果记录：`value`、`source`、`confidence`、`observed_at` 和 `user_confirmed`，UI 展示“名称提示”与“已识别类型”的区别。不要因 hostname 相同自动合并不同设备（这与现有身份策略一致）。

### 2. 采用分层、可解释的类型信号

优先级可以是：

1. **用户明确选择/确认**：最高权威，发现不能静默覆盖。
2. **设备管理 API / SNMP 自报的厂商、型号或平台**：只针对明确配置的目标，记录字段和规则。
3. **协议服务特征**：例如 mDNS 服务类型可提示打印、AirPlay 等能力；它通常证明“服务被公告”，不等价于硬件型号。mDNS 名称/服务由服务公告机制发布（[RFC 6762](https://www.rfc-editor.org/rfc/rfc6762)、[RFC 6763](https://www.rfc-editor.org/rfc/rfc6763)）。
4. **DHCP hostname/client-id、MAC OUI、反向 DNS 等启发式**：只给候选类型或厂商提示，低置信度，不用于自动合并或拓扑建模。

类型字典宜使用可扩展类别（router、switch、access point、computer、phone、printer、IoT、unknown），并将 `vendor`、`model`、`platform` 独立存储，避免一个 hostname 关键词同时充当全部字段。

### 3. 把采集源能力和运行状态区分

借鉴 OpenWrt capability detection 和 NetAlertX 插件来源：每种来源报告“支持/已配置/最近成功采集/最近错误”，结果携带来源标记。OpenWrt 本机接口事实和无线关联是可优先完善的无凭据来源；第三方路由器适配器需要用户提供凭据、限定请求目标，并明确验证状态。当前 Xiaomi adapter 的测试仅使用 fake device，尚未真实硬件验证，因此不能把 adapter 存在等同于兼容性已确认。

### 4. 保持拓扑主张受证据约束

Lanory 当前正确区分接口/桥成员、FDB MAC-port、ARP/DHCP 地址关联与 LLDP 邻居（见 [`docs/discovery.md`](discovery.md) 和 [`docs/topology.md`](topology.md)）。继续保持：

- 多个 MAC 出现在同一个端口，只能表示“该端口后方观察到多个 MAC”；**不得自动推断 switch/AP**。桥接 AP、路由器桥接、虚拟化主机及其他转发设备都可能产生该观测。
- LLDP 可支持“对端宣告了某 chassis/port/system name”；只有对端真实运行 LLDP 并且报文到达采集点才有结果。名称或能力字段也不应被扩大解释为完整拓扑。
- 本地 OpenWrt interface inventory 能说明 Lanory 所在路由器的 interface/bridge 关系；它不能识别另一个不暴露管理数据的盒子。
- 没有明确中间设备证据时保留 unresolved segment / 低置信度逻辑网络；用户添加并连接设备后记录为用户事实。

### 5. 主动扫描默认关闭，能力边界显式

Nmap 和 `arp-scan` 说明主动扫描可以补充静态/被动数据，但它会向网络目标发包，并受网段边界、过滤和客户端行为影响。保持 Lanory 当前 opt-in：由用户明确打开、限定 CIDR/接口、可配置速率/超时、展示扫描源与时间；不把扫描无响应解释为不存在或离线。ARP/NDP/FDB/DHCP 继续优先，因为它们读取路由器已有状态，且更符合 Lanory 的只读发现模型。

## 推荐顺序

| 顺序 | 工作 | 收益 | 风险/限制 |
| --- | --- | --- | --- |
| 1 | `typeHint` 降级为 hostname 提示，UI 展示来源/置信度 | 即刻减少误分类 | 需要前端明确区分 hint 与确认分类 |
| 2 | 增加可解释分类记录模型；把用户确认作为最高优先级 | 让多来源信号可以共存 | 需兼容现有 `device.type` 与迁移策略 |
| 3 | 为 mDNS 服务公告增加可审计类型提示；现有 opt-in 特性保持 opt-in | 不引入凭据，补充打印/媒体设备线索 | 服务是能力证据，不是型号证明；多播可见性有限 |
| 4 | OpenWrt 无线关联状态与接口来源状态细化 | 本机路由器场景证据最直接 | 驱动/平台差异，当前采集支持需在设备上验证 |
| 5 | 依据用户配置接入 SNMP 或受支持控制器 | 可较强识别网络设备/无线关联 | 凭据、安全范围、厂商/版本兼容性维护成本 |
| 6 | 评估可选主动扫描 | 补充本地网段响应主机 | 默认不运行；授权网段、扫描负载、误报/漏报均需处理 |

## 已核对的一手来源

- NetAlertX： [README](https://github.com/netalertx/NetAlertX/blob/main/README.md)、[Plugins](https://docs.netalertx.com/PLUGINS_OVERVIEW)、[Remote Networks](https://docs.netalertx.com/REMOTE_NETWORKS/)
- Nmap： [Host Discovery](https://nmap.org/book/man-host-discovery.html)、[OS Detection](https://nmap.org/book/osdetect.html)
- LibreNMS： [Initial Detection](https://docs.librenms.org/Developing/os/Initial-Detection/)、[Discovery Support](https://docs.librenms.org/Support/Discovery%20Support/)
- NetBox： [Device Type](https://netbox.readthedocs.io/en/stable/models/dcim/devicetype/)、[Device](https://netbox.readthedocs.io/en/stable/models/dcim/device/)、[项目文档源码](https://github.com/netbox-community/netbox/blob/main/docs/index.md)
- OpenWrt： [ubus](https://openwrt.org/docs/techref/ubus)、[iwinfo ubus](https://openwrt.org/docs/guide-developer/ubus/iwinfo)、[Wireless utilities](https://openwrt.org/docs/guide-user/network/wifi/wireless-tool/wireless.utilities)
- arp-scan： [官方项目/手册](https://github.com/royhills/arp-scan)
- IETF/IEEE： [RFC 826 ARP](https://www.rfc-editor.org/rfc/rfc826)、[RFC 2131 DHCP](https://www.rfc-editor.org/rfc/rfc2131)、[RFC 6762 mDNS](https://www.rfc-editor.org/rfc/rfc6762)、[RFC 6763 DNS-SD](https://www.rfc-editor.org/rfc/rfc6763)、[IEEE 802.1AB LLDP](https://standards.ieee.org/ieee/802.1AB/6844/)
