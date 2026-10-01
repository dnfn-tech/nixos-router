# 后端服务架构设计（docs-first）

本设计围绕“局域网内可写、即时应用”的家用路由 Web 管理界面：前端为静态资源，后端提供认证、配置读写、作业编排与系统应用。单一真源为 `/var/lib/nixos-router/config.json`，`/var/lib/nixos-router/state.db`（SQLite）承载会话/审计/作业/可选修订。

## 组件与进程
- Static UI（前端静态文件）
  - 由后端 API 以 `/` 挂载（或 NixOS `nginx`/`caddy` 反代静态），局域网可访问
- API 服务（LAN-only）
  - REST 接口：读取/更新配置、触发 apply、查询作业状态与审计、下载/恢复备份
  - 会话认证、授权与 CSRF 防护；生成作业记录
- Apply Worker（应用代理）
  - 串行消费作业：validate → 原子写 → 生成运行时配置 → reload/restart → 结果入库
  - 失败时不破坏当前生效配置；提供回滚与诊断信息

系统以 2 个 systemd 单元运行（可同一二进制不同子命令）：
- `nixos-router-api.service`
- `nixos-router-apply.service`

## 推荐技术栈与理由（首选）
- 首选：Go
  - 优点：单文件静态编译、内存占用低、标准库健壮；SQLite 生态成熟；Nix 打包友好
  - Web：`chi`（或 `echo`/`gin`），中间件完善；模板与静态文件内嵌简便
  - SQLite：`modernc.org/sqlite`（无 cgo）或 `mattn/go-sqlite3`（cgo）；启用 WAL 模式
  - 任务编排：基于 `context` 与 systemd 的 `ExecStartPost`/`notify`；或用本地队列（bolt/内存）+ SQLite 记录
- 备选：Rust（`axum` + `sqlx`）/ Python（`FastAPI` + `aiosqlite`）
  - Rust：零成本抽象、强类型；学习曲线更陡
  - Python：开发快；常驻内存与依赖体积偏大

## REST API 轮廓（v1）
- 认证
  - `POST /api/v1/session` 登录（表单：用户名/密码）→ Set-Cookie；写审计
  - `GET /api/v1/session` 查看会话；`DELETE /api/v1/session` 登出
- 配置（以 config.json 为核心资源）
  - `GET /api/v1/config` → 当前完整配置（可选支持 `?redact=true` 脱敏）
  - `PUT /api/v1/config` → 提交完整配置（或 `PATCH` 局部段）；返回作业 id
  - `POST /api/v1/apply` → 基于已保存配置触发一次应用（可用于快速重载服务）
- 资源分段（便于前端分步保存/校验，可选暴露）
  - `GET/PUT /api/v1/wan|lan|wifi|dns|firewall|nat|upnp|qos|parental|ddns|ipv6|ssh`
- 运行态与资产
  - `GET /api/v1/clients` 在线设备（来源 dnsmasq/hostapd/nftables 统计）
  - `GET /api/v1/jobs` / `GET /api/v1/jobs/{id}` 作业状态/日志
  - `GET /api/v1/audit` 审计列表（分页/筛选）
  - 备份：`GET /api/v1/backup/config`（仅 JSON）、`GET /api/v1/backup/full`（含 DB）
  - 恢复：`POST /api/v1/backup/restore`（上传包 → 校验 → 作业化）
  - 健康/指标：`GET /api/v1/healthz`、`GET /metrics`（Prometheus）
  - 能力探测：`GET /api/v1/capabilities/wifi`（返回 `nl80211` 接口组合、是否支持多 AP/DBDC、BSS 上限等；用于 UI/验证裁剪）

说明：推荐以“完整 JSON 提交 + 服务器端校验 + 原子写 + 作业编排”为主路径；细分资源用于表单分块与增量校验。

## `config.json` 结构（纲要）
```json
{
  "version": 1,
  "wan": {
    "mode": "dhcp | static | pppoe",
    "interface": "enp1s0",
    "static": { "address": "192.0.2.2/24", "gateway": "192.0.2.1", "dns": ["1.1.1.1"] },
    "pppoe": { "username": "", "password": "", "serviceName": "", "acName": "", "usePeerDns": true }
  },
  "lan": {
    "bridge": "br-lan",
    "ports": ["enp2s0", "enp3s0"],
    "ipv4": { "address": "192.168.88.1/24" },
    "dhcp": { "enable": true, "rangeStart": "192.168.88.100", "rangeEnd": "192.168.88.200", "leaseTime": "12h", "domain": "lan" },
    "staticLeases": [ { "enable": true, "hostname": "nas", "mac": "AA:BB:CC:DD:EE:20", "ip": "192.168.88.20", "note": "群晖" } ]
  },
  "wifi": {
    "bands": [
      { "band": "2g", "enable": true, "ssid": "nixos-router", "hidden": false, "channel": "auto", "width": "20", "encryption": "wpa2+wpa3", "password": "" },
      { "band": "5g", "enable": true, "ssid": "nixos-router-5G", "hidden": false, "channel": "auto", "width": "80", "encryption": "wpa2+wpa3", "password": "", "samePasswordAs2g": true }
    ],
    "guest": { "enable": false, "ssid2g": "", "ssid5g": "", "password": "", "isolation": true, "bandwidth": { "downMbps": 30, "upMbps": 10 }, "schedule": { "mode": "always|range", "from": "08:00", "to": "22:00" } },
    "bridgeToLan": true  // AP 接口桥接至 br-lan；访客通过 BSS 隔离 + 防火墙阻断访问 br-lan
  },
  "dns": { "upstreams": ["223.5.5.5", "119.29.29.29"], "domain": "lan" },
  "firewall": { "enable": true, "wanInput": "drop", "lanAllowTcp": [22, 53, 8080], "lanAllowUdp": [53, 67] },
  "nat": { "portForwards": [ { "enable": true, "name": "NAS Web", "proto": "tcp", "ext": "8443", "host": "192.168.88.20", "int": "443" } ] },
  "upnp": { "enable": false, "lanOnly": true },
  "qos": { "enable": true, "downMbps": 500, "upMbps": 50, "rules": [ { "enable": true, "target": "192.168.88.118", "prio": "normal", "down": "20", "up": "5" } ] },
  "parental": { "rules": [ /* 周历/对象规则 */ ] },
  "ddns": { "provider": "cloudflare|duckdns|aliyun|custom", "options": {/* 各自字段 */}, "interval": "10m" },
  "ipv6": { "enable": false, "wan": { "mode": "dhcpv6|slaac|pppoe" }, "lan": { "pd": "auto", "ra": true, "assign": "slaac", "dnsAdvertise": true }, "fw": { "wanInput": "drop", "icmpv6Essential": true } },
  "ssh": { "enable": true, "allowPassword": false, "allowRoot": false, "wheelPasswordlessSudo": true },
  "system": { "uiPort": 8080 }
}
```

注：敏感字段（如 PPPoE/WiFi/DDNS 凭据）位于 `config.json` 内，但该文件仅存本机 `/var/lib/nixos-router`；提供“脱敏导出”接口。

## Apply 管道（关键路径）
1) Validate
   - JSON Schema 校验 + 语义校验（IP 段/端口/冲突检测/范围检查）
   - WiFi 能力校验：根据 `nl80211` 能力限制并发 AP / 频道组合；若 `AP≤1` 则只允许单 AP，并将访客作为 BSS（若硬件支持），否则禁用
2) Atomic write
   - 写入 `config.json.tmp` → `fsync` → `rename()` 覆盖 `config.json`；同时记录作业条目
   - 维护 `last-good.json`（或版本指针）以便快速回退
3) Generate runtime configs
   - nftables（含 IPv6）/ dnsmasq（DHCP+DNS 主后端）/ hostapd / ppp / sysctl / miniupnpd / cake 等
   - WiFi AP 接口桥接入 `br-lan`；访客 SSID 通过 `bss`/`vap` 生成并启用客户端隔离与防火墙规则阻断访问 `br-lan`
   - 写入到 `/run/nixos-router/*.conf` 或 `/etc/...`（按 NixOS 约定），必要时生成 unit drop-in
4) Reload/Restart
   - 优先 `reload`；不支持热加载时 `restart`；保持串行顺序与依赖（例如：先网络后服务）
5) Audit & Result
   - 将 apply 结果（成功/失败、耗时、摘要）写入 `state.db`；必要时保留生成的配置快照用于诊断
6) Failure & Rollback
   - 失败时不替换当前生效的运行时配置；支持回退到 `last-good.json` 再次 apply；错误信息通过作业接口暴露

## 密钥/机密存放
- 默认存放在 `config.json` 的对应字段；仅位于设备 `/var/lib/nixos-router/`，不进入 flake/git。
- 提供两类导出：
  - 标准导出（含所有字段，用于完整备份，需二次确认）
  - 脱敏导出（遮蔽密钥字段，用于分享/排障）
- 可选旁路：支持 `secrets.json` 覆盖某些字段（优先级高于 `config.json`），但默认不要求。

## 会话认证模型
- 单管理员账户（后续可扩展）：用户名/密码登录
- Cookie-Session（HttpOnly、SameSite=Lax/Strict），CSRF 令牌（双提交/同源）
- 密码散列：argon2id（或 bcrypt），登录失败限速
- 仅在 LAN 监听；防火墙自动放行 8080（可改）；WAN/IPv6 默认拒绝
- 审计范围：登录/登出、配置读取/修改、apply、备份/恢复、下载

## NixOS 模块集成
- 入口开关（建议命名）
  - `services.nixosRouter.webui.enable = true;`
  - `services.nixosRouter.webui.port = 8080;`
  - `services.nixosRouter.stateDir = "/var/lib/nixos-router";`
- 模块职责
  - 安装 API/Apply 二进制与前端静态资源；写入 systemd 单元（LAN-only 监听）
  - 定义目录与权限（`StateDirectory=nixos-router`）；确保 `/var/lib/nixos-router` 持久
  - 开机（activation）时从 `config.json` 触发一次幂等 apply（若存在）
  - 远程 `nixos-rebuild` 更新模块/程序时，不触碰 `stateDir`

## 可观测性与日志
- 结构化日志（JSON 行）：组件、作业 id、阶段、耗时、错误码
- journald 集成，必要时输出到文件（`/var/log/nixos-router/`）
- 指标：`/metrics`（Prometheus）——请求量/延迟、apply 用时、失败率、会话数
- 诊断包：导出最近 N 次作业的生成配置与日志

## 测试策略（概述）
- 单元测试
  - Schema 校验、字段语义校验（IP/端口/范围/冲突）
  - 生成器：将 `config.json` 映射为 nftables/dnsmasq/hostapd 等片段（Golden tests）
- 集成测试
  - NixOS VM 测试（QEMU）：API 可用、保存/应用成功、服务联动（dnsmasq/nftables/hostapd）
  - 断网/失败注入：生成失败、服务重启失败、回滚路径
- 端到端（E2E）
  - UI 自动化（只读 → 可写 → 状态机 → 作业完成）
  - 备份/恢复/重启链路

## 一致性说明
- 本架构与 `docs/requirements.md` 的 IA/功能与 UX 状态机一致
- 存储方案与 PR 中的 `docs/storage-choice.md`（混合：JSON + SQLite）一致；本文不再重复 rationale

## 插件 / 功能模块（Plugin / Feature Module）

为保证后续功能可扩展且不重写核心，后端采用“稳定内核 + 功能模块”的架构。核心仅提供通用能力：配置注册、作业队列、审计、会话/鉴权、设备与状态查询、机密存取；具体网络功能（如 WAN、LAN、WiFi、Firewall、QoS、Parental、DDNS、IPv6 等）以“内置一方模块”的方式实现，并为将来第三方模块预留扩展点。

### 合同（Contract）
- Manifest（模块自描述）
  - `id`：唯一标识（短横线/小写），如 `wan`、`wifi`、`ddns`、`qos`
  - `name`：显示名
  - `version`：语义化版本
  - `apiRoutes`：该模块向核心路由器注册的 REST 路由（前缀 `/api/plugins/<id>/...`）
  - `ui`：导航项与页面挂载点声明（前端静态资源路径或路由片段）
  - `configSchema`：JSON Schema 片段（合并到总 schema 的 `plugins.<id>` 节点）
  - `applyHooks`：声明实现的钩子（`preValidate` / `postValidate` / `generate` / `preReload` / `postReload`）
  - `capabilities`：需要的能力/权限（如需访问客户端列表/机密读取等）
- Go 接口草图（示意）

```go
// 插件主接口：暴露 Manifest 与注册期回调
type Plugin interface {
  Manifest() Manifest
  Register(core CoreRegistry) error // 注入路由、配置段、钩子、UI、指标等
}

type Manifest struct {
  ID           string
  Name         string
  Version      string
  APIRoutes    []APIRoute
  UI           UIContribution
  Capabilities []Capability
}

type UIContribution struct {
  NavItems []NavItem // 侧栏入口，如分组/排序/图标
  Pages    []PageDef // /ui/plugins/<id>/* 静态资源或挂载点
}

type NavItem struct {
  ID    string // 如 "wifi"
  Label string // "WiFi"
  Group string // "基础" / "网络服务" / "系统"
  Icon  string // 可选
  Order int    // 可选
  Page  string // 关联的页面 ID
}

type PageDef struct {
  ID     string // "wifi"
  Route  string // "/wifi"
  Title  string // "WiFi"
  Asset  string // 前端组件/静态资源路径
}

// 配置段：各模块自行声明 schema/默认值与校验
type ConfigSection interface {
  Namespace() string      // 如 "plugins.ddns" 或内置 "wan"
  JSONSchema() []byte     // 单段 schema，核心合并
  DefaultConfig() any
  Validate(cfg any) error
}

// Apply 生命周期钩子：Worker 在统一的作业中按序调用
type ApplyHook interface {
  PreValidate(ctx context.Context, cfg *Config) error
  PostValidate(ctx context.Context, cfg *Config) error
  Generate(ctx context.Context, cfg *Config, out Dir) error // 生成 nftables/dnsmasq/hostapd/ppp 等片段
  PreReload(ctx context.Context, cfg *Config) error
  PostReload(ctx context.Context, cfg *Config) error
}

// 核心可被注入/调用的稳定服务接口（节选）
type CoreRegistry interface {
  Router() Mux                                // 注册路由（统一鉴权/CSRF 中间件链）
  RegisterConfig(cs ConfigSection) error      // 注册配置段
  RegisterApply(h ApplyHook) error            // 注册 apply 钩子
  RegisterUI(ui UIContribution) error         // 注册导航与页面定义
  Secrets() SecretStore                       // 机密读取/写入（受权限）
  Jobs() JobQueue                             // 提交/查询作业
  Audit() AuditLogger                         // 记录审计事件
  Clients() ClientsProvider                   // 在线设备与统计
}
```

### 生命周期
1) Discover：核心扫描已编译内置注册表与（可选）外置目录，找到可用插件
2) Register：读取 manifest，注册路由/配置段/schema/钩子/UI
3) Enable/Disable：更新启用状态（v1 推荐“冷加载”：开关后重启 API/Apply 服务）
4) Configure：在 `config.json` 的 `plugins.<id>` 下写入配置；保存后进入统一 apply 作业
5) Apply Hooks：按“preValidate → postValidate → generate → preReload → postReload”顺序调用；任何一步失败均中止并返回错误

### 隔离与稳定内核
- 安全边界
  - 插件注册的 API 路由通过核心中间件统一处理：LAN-only 监听、会话鉴权、CSRF、防火墙策略不可绕过
  - 访问机密/作业/审计/设备信息等能力需在 manifest 中声明 `capabilities`，核心按最小权限发放
- 稳定服务 API
  - 配置注册/总 schema 合并、作业队列、审计日志、在线设备查询、机密存储
  - 插件不得自行直接操作系统服务（如直接重启 `dnsmasq`），一律通过 `ApplyHook.Generate` 产出配置 → 核心统一 reload/restart

### 打包与分发
- 一方（内置）模块：随核心同仓编译进同一 Go 二进制（注册表静态链接），最小化部署复杂度（v1 推荐）
- 三方（外置）模块：后续版本提供“本地子进程”协议（如 JSON-RPC over stdio）与静态资源目录约定
- NixOS 选项
  - `services.nixosRouter.plugins = [ "wan" "lan" "wifi" "firewall" "qos" "parental" "ddns" "ipv6" ];`
  - 外置模块可通过 Nix 包装放入约定目录（如 `/run/nixos-router/plugins`），由核心 Discover
- 资源交付
  - 二进制：内置编译；外置单独 derivation
  - 前端：每个插件可携带 `/ui/plugins/<id>/` 静态资源，核心统一挂载

### 热/冷加载
- v1 建议：启用/禁用插件后，重启 `nixos-router-api` 与 `nixos-router-apply`（冷加载），保证一致性与最小实现成本
- 后续可选：在不变更路由/中间件链的前提下支持热加载 UI 与钩子

### 配置合并（Config Merge）
- 核心 `config.json` 增加命名空间：`plugins.<id>`；插件暴露的 `configSchema` 合并入总 schema
- 保存时：核心校验“内核段 + 各插件段”后整体原子写；apply 时统一串行执行所有启用插件的钩子
 - UI：仅导出已启用插件的 `ui.nav`/`ui.pages` 到 `GET /api/v1/ui/nav`；禁用插件的路由返回 404/disabled

### UI 相关 REST
- `GET /api/v1/plugins`：列出插件清单（manifest + enabled）
- `PATCH /api/v1/plugins/{id}`：设置启用状态（v1 触发冷加载提示）
- `GET /api/v1/ui/nav`：汇总返回当前“可见”的导航与页面定义（含分组与顺序）

### 模块化映射（内置一方）
- 将当前功能以模块形态实现，即使在 v1 作为“内置”：
  - 基础：`wan`、`lan`、`wifi`、`firewall`、`nat`/`upnp`、`ddns`、`ipv6`、`ssh`、`system`
  - 可选：`qos`、`parental`、`adblock`（广告过滤，基于 dnsmasq 列表）、`traffic`（流量统计，vnstat 或 nft/conntrack 聚合）、`mihomo`（Clash Meta 代理）、`vlan`（多 VLAN/LAN）
  - 可选：`tailscale`（远程接入/overlay）、`zerotier`（远程接入/overlay）
- 好处
  - 职责清晰、边界明确；后续替换/增强任一模块不影响核心与其他模块
  - 第三方仅需遵守合同与能力声明，即可新增业务能力（例如广告过滤、VPN、报表等）

### WiFi 设计要点（能力驱动并发 + 单一 LAN）
- 单一 LAN：主 WiFi SSID 的 AP 接口桥接进 `br-lan`，与有线同网段/同 DHCP（dnsmasq）/同 DNS
- 能力探测：后端基于 `nl80211`/`iw` 获取接口组合与 AP/BSS 能力，作为 UI 与校验的依据
- 单 AP 设备（如 MT7927 `AP≤1`）：仅暴露单 AP；访客网络以附加 BSS 形式呈现（若硬件允许 BSS），否则禁用
- 多 AP/DBDC 设备：可同时启用 2.4G + 5G，或同频多 SSID；频道/带宽组合受硬件与监管域约束

### 禁用插件的 Apply 行为
- 禁用状态的插件在下一次 apply 中：
  - 不再执行其 `ApplyHook`，不生成对应运行时配置
  - 核心在 `preReload`/`postReload` 阶段根据需要撤销/下线相关服务与规则（例如移除对应 nftables chain、停止相关 unit）
  - 审计记录“禁用插件导致的配置撤销”，便于追溯

### 可选模块的 Apply 要点（摘要）
- adblock
  - 写入 `dnsmasq.d/adblock.conf` 或等价生成文件（如 `address=/domain/0.0.0.0`），并在状态目录缓存下载的列表；执行 `dnsmasq` reload
  - 禁用时移除/停用相关片段并 reload
- traffic
  - 选择 `vnstat` 作为首选采集器（或在 `collectors` 中选择 `nft/conntrack` 聚合），由模块管理其 unit/定时器；读取统计供 UI 展示
  - 禁用时停止采集器服务，保留历史数据由保留策略决定
- mihomo
  - 生成 mihomo YAML 配置到状态目录；管理 `mihomo.service`；可选启用 TUN 并配置路由策略
  - DNS 交互：在 `redir-host`/`fake-ip` 模式下可将 dnsmasq 上游指向 mihomo 的 DNS，同时保留本地域名在本机解析；`fake-ip` 模式需对本地域名/特殊域名做绕过
  - 禁用时停止服务并撤销相关路由/规则
- vlan
  - 基于 802.1Q 创建 VLAN 子接口并加入各自桥（如 `br-vlan10`），为每个 VLAN 配置 IPv4/子网；生成对应 dnsmasq 池与域名设置；按 `isolate` 生成/撤销 nftables 隔离规则
  - 插件关闭时回到默认单一 `br-lan` 拓扑
- tailscale
  - 管理 `tailscaled` 服务与 `tailscale up` 参数；当 `controlPlane=selfhost` 时追加 `--login-server=$loginServer`（Headscale）；按 `advertiseRoutes` 通告局域网段；配合 nftables 放行必要端口/协议；禁用停止服务并撤销路由
- zerotier
  - 管理 `zerotier-one` 服务；当 `controlPlane=selfhost` 时将客户端指向 `controllerUrl` 并使用 `apiToken`；加入/离开 `networks[]`；按 `managedRoutes` 配置路由；配合 nftables 放行必要端口/协议；禁用停止服务并撤销路由

