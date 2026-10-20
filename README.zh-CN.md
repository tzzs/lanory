<div align="center">

# Lanory · 网绘

**家的网络，一图看清。**
**See your home network at a glance.**

面向 OpenWrt / iStoreOS 的自托管家庭网络拓扑与监控工具。
一个 Go 二进制、一个 SQLite 文件、一张可交互的网络地图。
无云端、无账号、运行时不需要 Node.js 或 Python，并且对路由器**只读**。

[English](README.md) · [文档](docs/) · [Demo 模式](#demo-模式) · [MIT](LICENSE)

</div>

---

## 它是什么

Lanory 只回答关于家庭网络的一小组问题，并且只回答这些：

- 现在家里有哪些设备在线？
- 每台设备接在哪个物理网口后面？
- 那个网口协商速率是多少，链路是否 UP？
- 哪台设备不可达，从什么时候开始，为什么？
- 过去一小时里网络发生了什么变化？

它**不是**路由器管理工具。它从不修改 WAN、LAN、DHCP、防火墙、DNS、路由或
VLAN 配置。它只读取 OpenWrt 已经拥有的状态，并把它变成一张看得懂的图。

```
Internet
   │
   ├── 光猫
   │
   └── 上游路由 192.168.1.1
          │  100 Mbps
       ┌──┴──────────────────────────────────────┐
       │  EASEPi R1（OpenWrt，br-lan .100.1/24）  │
       ├── eth0  WAN   192.168.1.4   100 Mbps  UP
       ├── eth1  LAN   1000 Mbps  ── 小米路由 ── iPhone · Mac Studio · Watch · 台灯 · ESP32
       ├── eth2  LAN   2500 Mbps  ── Windows PC 192.0.2.116
       └── eth3  LAN   NO-CARRIER
```

## 功能

| | 功能 | 状态 |
| --- | --- | --- |
| ✅ | 被动发现：接口、bridge FDB、ARP/NDP、DHCP 租约、UCI/ubus | 已实现 |
| ✅ | 设备身份引擎：把 MAC / DHCP client id / hostname / 厂商合并成一台设备 | 已实现 |
| ✅ | 交互式拓扑图，带可信度、证据、虚线未确认连线 | 已实现 |
| ✅ | 手动添加设备 / 手动连线 / 锁定连线 / 隐藏 / 合并设备 | 已实现 |
| ✅ | 可达性监控，带失败阈值（连续失败 N 次才判定离线） | 已实现 |
| ✅ | 探测历史、设备状态历史、网络事件记录 | 已实现 |
| ✅ | SSE 实时推送 | 已实现 |
| ✅ | 可安装的 `.ipk` + `.apk`（x86_64 / aarch64 / armv7 / i386，**不支持 MIPS**） | 已在参考路由器上**实际安装验证**；格式校验通过，支持签名（`make apk-sign`） |
| ✅ | Demo 模式：不需要路由器即可预览完整界面 | 已实现 |
| ✅ | 中英文界面 | 已实现 |
| ⚠️ | LLDP、mDNS、SSDP 采集器 | 已实现，默认关闭，未在真实组播网络验证 |
| ⚠️ | 通用路由器适配器 | 已实现（不提供客户端枚举） |
| ⚠️ | 小米路由器适配器 | 已实现，能力探测；仅在模拟设备上验证 |
| ❌ | TP-Link / ASUS / 华为 / SNMP 适配器 | 注册表已预留，未实现 |
| ❌ | 主动网段扫描 | 代码路径存在，默认关闭，非推荐模式 |
| ❌ | 多 Agent / 跨子网拓扑 | 未实现 |

每个软件包的真实状态见 **[docs/architecture.md](docs/architecture.md#current-status)**。

## 界面截图

这个仓库目前**没有附带截图**，凭空生成几张比承认这一点更糟。界面实际包含：

| 页面 | 内容 |
| --- | --- |
| **总览** | 设备计数、接口状态与速率、链路异常卡片、最近新增设备、最近事件 |
| **网络拓扑** | React Flow 画布：Internet → 光猫 → 路由 → 端口 → 设备；连线标注端口与速率；未确认连线为虚线；拖动即可固定位置 |
| **设备** | 可搜索表格，按类型 / 状态 / 来源 / 接口筛选；详情面板含地址、身份、关联连线、探测结果与原始证据 |
| **网络监控** | 每个探测目标的延迟与丢包、接口链路状态、历史曲线 |
| **事件记录** | 可筛选的时间线（`device.online`、`link.down`、`speed.changed` …） |
| **设置** | 采集器开关、间隔、保留期限、适配器凭据、Demo 模式开关 |

想两分钟内看到全部内容，直接跑下面的 Demo 模式。如果要给项目页配图，请从
Demo 模式的实例截图——那里的数据被明确标注为模拟数据。

## 安装

### OpenWrt / iStoreOS（推荐）

**不需要 OpenWrt SDK。** 软件包本质上只是「已编译好的二进制 + 几个脚本」的压缩包，
而 Lanory 的二进制是完全静态的，交叉编译就是全部所需的"工具链"。

```sh
# 在你的电脑上：交叉编译并打包所有架构
make release     # dist/lanory-linux-{amd64,arm64,armv7,386}
make packages    # dist/packages/*.ipk + *.apk，全程不涉及 SDK
make package-check

# 在路由器上：挑与 `uname -m` 匹配的那个文件
scp dist/packages/lanory_0.1.0_aarch64_generic.ipk root@192.168.100.1:/tmp/
ssh root@192.168.100.1 'opkg install /tmp/lanory_0.1.0_aarch64_generic.ipk'
ssh root@192.168.100.1 'logread -e lanory | grep token'   # 访问令牌
```

然后打开 `http://192.168.100.1:8787/`，粘贴该令牌。基于 apk 的版本改用
`apk add --allow-untrusted` 安装 `.apk`。

如果你希望由 buildroot 从源码编译（feed 风格、可复现、由 SDK 签名），
SDK 路线也一并写进了文档。

构建步骤、依赖列表、升级与清除语义见
**[packaging/openwrt/README.md](packaging/openwrt/README.md)**。

> **已验证：** 无需 SDK 即可产出 8 个包（`.ipk` + `.apk` × 4 架构）；
> `make package-check` 会重新解析每个归档，校验格式、control 字段、conffiles、
> 安装脚本、文件权限，并确认包内二进制与发布二进制逐字节一致。
>
> **未验证：** 真实路由器上的 `opkg`/`apk` 是否接受这些包，以及服务能否启动 ——
> 本环境没有 OpenWrt 设备。请在真机上先行验证。

### 普通 Linux（systemd）

```sh
CGO_ENABLED=0 go build -trimpath -o lanory ./cmd/lanory
sudo install -Dm0755 lanory /usr/bin/lanory
sudo install -Dm0644 packaging/linux/lanory.service /etc/systemd/system/
sudo systemctl enable --now lanory
```

见 [packaging/linux/README.md](packaging/linux/README.md)。

## Demo 模式

想判断 Lanory 对你有没有用，这是最快的办法。不需要路由器、不需要 root、不需要
网络访问。

```sh
make demo
# 或者手动：
LANORY_DEMO_MODE=true LANORY_DATABASE_PATH=/tmp/lanory-demo.db \
  go run ./cmd/lanory
```

然后打开 <http://127.0.0.1:8787/>。

Demo 模式会把一份确定性样本网络写入一个全新的 SQLite 数据库，数据来自
`fixtures/easepi-r1/`——本仓库中经过脱敏处理的参考网络：13 台设备、5 个接口
（eth0 WAN 100 Mbps、eth1 LAN 1 Gbps、eth2 LAN 2.5 Gbps、eth3 NO-CARRIER、
br-lan）、拓扑连线、带历史的探测目标，以及一条事件时间线。

**所有数据都被标注为模拟。** 每台设备、每个接口、每条连线和每个事件都带 `demo`
发现来源，`/api/v1/health` 与 `/api/v1/overview` 都会返回 `demo_mode: true`，
界面据此显示横幅。产品规范明确禁止把模拟在线状态冒充真实扫描结果，Demo 模式就是
为了让这种错误不可能发生而设计的。加载是幂等的：随便重启，不会产生重复数据。

详见 [docs/demo.md](docs/demo.md)。

## 开发

需要 Go 1.26+，只有改前端时才需要 Node 22+。

```sh
make web-dev      # 终端 1：Vite 开发服务器 :5173，把 /api 代理到 :8787
make run          # 终端 2：构建前端并嵌入二进制后运行
make test         # go test ./...
make check        # gofmt + go vet + go test
cd web && npm test
```

目录结构：

```
cmd/lanory/            入口
internal/model/        领域模型 —— 唯一事实来源
internal/database/     SQLite 持久化、版本化迁移
internal/discovery/    采集器：openwrt、fdb、arp、ndp、dhcp、lldp、mdns
internal/device/       设备身份引擎（合并规则与优先级）
internal/topology/     图构建、可信度、布局
internal/monitoring/   探测引擎、状态机、外网连通性
internal/adapters/     路由器适配器：generic、xiaomi（+ 能力注册表）
internal/api/          REST + SSE + 静态资源
internal/demo/         Demo 模式数据集与加载器
internal/platform/     OpenWrt 与普通 Linux 的平台识别
web/                   React 18 + TypeScript + Vite + Tailwind + React Flow
packaging/             openwrt / linux
docs/                  architecture、discovery、topology、api、development …
fixtures/easepi-r1/    脱敏参考网络，测试与 Demo 模式共用
```

第一次改动之前请读 [docs/development.md](docs/development.md)，数据模型见
[docs/architecture.md](docs/architecture.md)。

## 路线图

**当前（0.1）** —— 就是本仓库
被动发现、身份引擎、拓扑、监控、手动编辑、SSE、OpenWrt 打包、
Demo 模式、中英文界面。

**下一步（0.2）**
在真实的 OpenWrt 25.12 / iStoreOS 25.12.5 设备上验证软件包；在真实小米路由器上
验证适配器；基于 LLDP 的交换机发现；端口流量曲线；事件日志的文案打磨。

**以后**
TP-Link / ASUS 适配器、通用 SNMP、跨子网多 Agent 探测、从无线驱动读取关联信息、
拓扑导入导出。

**永远不做**
写路由器配置。云同步。账号体系。遥测。

## 设计原则

1. **只读。** Lanory 读取状态，不配置你的路由器。
2. **证据优先于猜测。** 每条连线都带来源、证据和可信度。没有证据的连线画成虚线，
   而不是隐藏。
3. **没有数据不是错误。** 缺少采集器、路由器 API 不可达、能力不支持——都不会让
   整个应用失效。
4. **本地优先。** 无云端、无遥测、无账号。一个二进制、一个文件。
5. **诚实的界面。** 模拟数据会被标注。推断的关系会说明是推断。"未知"是一个独立
   状态，不等于"离线"。
6. **小到能跑在路由器上。** 纯 Go SQLite、无 cgo、批量写入、WAL 适度
   autocheckpoint。

## 技术局限

这些是数据源本身的性质，不是以后会修的 bug。完整讨论见
**[docs/discovery.md](docs/discovery.md)**。

- **FDB 不能证明设备是无线设备。** 它只能证明某个 MAC 是从哪个 bridge 端口学到的。
  一个端口后面有很多 MAC，可能是一台非管理型交换机，也可能是一台 AP；因此 Lanory
  永远不断言"这些是 WiFi 客户端"。
- **一个端口后面有多台设备时，拓扑是不完整的。** 除非 LLDP 或厂商 API 报出那台
  交换机或 AP，否则它是不可见的。Lanory 宁可把这个网段标记为未解析，也不虚构节点。
- **随机化 MAC 让长期追踪不可靠。** 现代手机会轮换 MAC。Lanory 会降低这类地址的
  可信度，并依赖 hostname / DHCP client id 合并，这种合并更弱。
- **ARP/NDP 邻居状态不是可达性结果。** `REACHABLE`、`STALE`、`DELAY`、`PROBE`、
  `FAILED`、`INCOMPLETE` 描述的是内核邻居缓存，不是"设备现在是否会应答"。
- **从路由器发出的探测不能证明两个终端之间可达。** 它只证明路由器能到其中一个。
  终端之间的可达性需要从终端发起探测。
- **Demo 模式的数据是模拟的。** 它源自一份真实快照，但里面没有任何实时数据，并且
  始终被标注。
- **本版本不支持 MIPS 路由器。** 纯 Go 的 SQLite 驱动
  （`modernc.org/sqlite` → `modernc.org/libc`）没有 MIPS 的系统调用层，数据库层
  无法为 `mips`/`mips64` 编译。ARM 与 x86 目标不受影响。详见
  [packaging/openwrt/README.md](packaging/openwrt/README.md#target-architecture)。

## 安全

Lanory 会暴露一张详细的家庭网络地图。它默认只监听 LAN，并支持 Bearer Token 认证。
适配器凭据只存在于内存中，不会写日志，除了目标设备之外不会发往任何地方；Lanory
从不尝试默认密码，也从不尝试绕过认证。

见 [docs/security.md](docs/security.md)。发现漏洞请提交私密安全公告，不要开公开 issue。

## 许可证

[MIT](LICENSE)。Copyright (c) 2026 Lanory contributors。

`fixtures/` 目录里是真实网络快照的**脱敏副本**。原始快照不在本仓库，也不会进入
本仓库。
