# 插件与功能模块总览

本项目后端采用“稳定核心 + 模块/插件”的扩展模型：核心仅提供通用能力（配置注册、作业队列、审计、会话/鉴权、机密、在线设备），各网络功能以模块实现（v1 作为内置一方模块编译进二进制），并为将来第三方插件预留扩展点与协议。

- 合同：Manifest（id/name/version/routes/ui/configSchema/applyHooks/capabilities）
- 生命周期：discover → register → enable/disable → configure → apply（pre/post validate、generate、reload）
- 隔离：统一中间件链（LAN-only、鉴权、CSRF）；能力按需授权；生成配置由核心统一 reload/restart
- 打包：v1 内置模块随核心编译；第三方将来可作为子进程插件（JSON-RPC over stdio）与静态资源目录
- 配置：插件配置位于 `config.json` 的 `plugins.<id>`；schema 片段合并入总 schema
- 热/冷：v1 建议启用/禁用后重启 API/Apply（冷加载）；热加载后续评估

详细设计、Go 接口草图与示例模块映射请见 `docs/backend-architecture.md` 的“插件 / 功能模块”章节。

## UI / 页面控制

为避免前端菜单硬编码、支持按模块裁剪功能，WebUI 导航与页面由插件/功能模块贡献并由后端汇总。

### Manifest 中的 UI 声明
- `ui.nav[]`（侧栏入口，按分组/顺序渲染）
  - 结构：`{ id, label, group, icon?, order?, page }`
    - `group`：如「基础」「网络服务」「系统」
    - `page`：点击后跳转的页面 id（由 `ui.pages` 注册）
- `ui.pages[]`（页面注册）
  - 结构：`{ id, route, title, asset }`
    - `route`：如 `/wifi`、`/ddns`，前端路由映射
    - `asset`：组件或静态资源路径（由前端构建/静态目录提供）
- 预留：`ui.sections[]`（向现有页面注入分区/面板），v1 可延后实现，仅作为文档占位

### 启用/禁用与可见性
- 启用的插件：其 `ui.nav` 与 `ui.pages` 对应的导航与页面可见；相关 API 路由可用
- 禁用的插件：隐藏对应导航与页面；其 API 路由返回 404/disabled
- 内置“核心页面”不可在运行期禁用（或仅能通过编译期构建标记禁用）：
  - 概览（overview）、WAN、LAN、WiFi、设备（clients）、DNS、防火墙（firewall）、SSH、系统（system）、插件管理（plugins-mgmt）
- 可选内置（QoS、家长控制、DDNS、IPv6 …）：与插件 1:1 对应，可通过 `config.json` 的 `plugins.<id>.enabled` 进行运行期开启/关闭

### 前端加载方式
- 前端通过 `GET /api/v1/ui/nav` 或 `GET /api/v1/plugins`（返回 manifest + enabled）拉取导航/页面定义
- Shell/导航不硬编码完整功能清单，而是根据返回结果动态绘制
- v1 冷加载：启用/禁用后可能需要 API 进程重启；在 apply 作业完成后刷新前端即可生效

## 可选内置模块示例

### AdBlock（广告过滤）
- 目标：基于 DNS 层的拦截（dnsmasq），支持引入常见 Blocklist；后续可选 AdGuard Home 方案
- 配置：`plugins.adblock`（例如）
  - `enabled`：是否启用
  - `lists[]`：订阅 URL 或内置集合名（后端解析为 dnsmasq 可读格式）
  - `mode`：`hosts` / `address` / `custom`（生成 `address=/domain/0.0.0.0` 等）
  - `updateInterval`：列表刷新周期（可选）
- UI：侧栏“网络服务”分组新增“广告过滤”，页面展示订阅、启用状态与（later）统计
- Apply：下载/刷新列表至状态目录（不进入 `config.json`），生成 `dnsmasq.d/adblock.conf` 或等价片段；`dnsmasq` reload

### Traffic（流量统计）
- 目标：接口/客户端的流量统计与可视化（vnstat，或基于 nft/conntrack 聚合）
- 配置：`plugins.traffic`（例如）
  - `enabled`：是否启用
  - `retentionDays`：保留天数（或数据源自身保留策略）
  - `collectors[]`：`vnstat` / `nft` / `conntrack`（可选其一或组合，v1 选择保守方案）
- UI：侧栏“网络服务”分组新增“流量统计”，页面展示开关、保留期与图表（后端提供只读数据）
- Apply：启停采集器相关服务/定时任务；读路径为只读 API（查询统计），写路径主要用于 `enabled` 切换

### Mihomo（Clash Meta 代理）
- 目标：提供基于 mihomo（Clash Meta）的代理能力，可选启用；默认不属于核心路径
- 配置：`plugins.mihomo`（例如）
  - `enabled`：是否启用
  - `profile`：配置来源（文件路径或订阅 URL）
  - `mode`：`redir-host` / `fake-ip`
  - `tun.enable`：是否启用 TUN（可选）
  - `dns.port`：mihomo 内部 DNS 端口（与 dnsmasq 的交互需谨慎）
  - `updateInterval`：订阅/规则更新周期（可选）
- UI：侧栏“网络服务”分组新增“代理（Mihomo）”，页面包含启用/配置/状态；（later）连接统计
- Apply：生成 mihomo 配置文件至状态目录并管理 `mihomo.service`；如 `tun.enable=true` 则创建/管理 TUN；与 dnsmasq 的交互：\n  - 在 `redir-host`/`fake-ip` 场景下，可将 dnsmasq 上游指向 mihomo 的 DNS（保留内网域名在本机解析）\n  - `fake-ip` 模式需在文档中标注注意事项（本地域名绕过、可能的冲突处理）\n- 禁用：隐藏页面并在下一次 apply 停止服务

### VLAN（虚拟局域网，多 LAN 可选）
- 目标：在保持默认“单一 br-lan”的前提下，通过可选插件提供多 VLAN/LAN 的能力
- 配置：`plugins.vlan`（例如）
  - `enabled`：是否启用
  - `vlans[]`：`{ vid, name, bridge, ipv4.address, dhcp.{enable,rangeStart,rangeEnd,leaseTime,domain}, isolate }`
  - `lan.ports`：可选的端口-VID 映射/打标（具体结构依实现细化）
- UI：侧栏“网络服务”分组新增“VLAN”，页面用于新增/编辑/删除 VLAN，设置子网/DHCP/隔离；（later）将 WiFi BSS 映射到某 VLAN
- Apply：为每个 VLAN 创建/更新 802.1Q 子接口与桥，生成 dnsmasq 的每 VLAN 段配置，按 `isolate` 生成 nftables 隔离规则；默认插件关闭时维持单一 `br-lan`

### Tailscale（可选远程接入）
- 目标：通过 Tailscale 提供远程接入/overlay 网络能力（可选），谨慎发布局域网路由
- 配置：`plugins.tailscale`（例如）
  - `enabled`：是否启用
  - `controlPlane`：`official | selfhost`（自建控制面使用 Headscale）
  - `loginServer`：当 `controlPlane=selfhost` 时必填（Headscale URL，如 `https://headscale.example.com`）
  - `authKey`：可选一次性授权秘钥（建议通过机密存储而非明文）
  - `advertiseRoutes[]`：对外通告的 CIDR（如 `192.168.88.0/24`），需配合防火墙策略
  - `acceptRoutes`：是否接受来自管理面的路由下发
  - `userspaceNetworking`：可选（视设备能力）
- UI：侧栏“网络服务”分组新增“Tailscale”，管理启用/登录状态/路由通告
- Apply：管理 `tailscaled`/`tailscale up`（当 `selfhost` 时追加 `--login-server=$loginServer`），根据配置通告/接受路由；nftables 放行必要流量；禁用时停止服务并撤销路由

### Zerotier（可选远程接入）
- 目标：通过 Zerotier 提供 overlay 网络能力（可选），谨慎加入网络与路由通告
- 配置：`plugins.zerotier`（例如）
  - `enabled`：是否启用
  - `controlPlane`：`official | selfhost`
  - `controllerUrl`：当 `controlPlane=selfhost` 时用于指向自建控制器的 API/控制端点
  - `apiToken`：访问自建控制器所需凭据（建议机密存储）
  - `networks[]`：加入的网络 ID 列表
  - `managedRoutes[]`：需要通告/安装的路由（按网络）
  - `authToken`：可选，用于本机控制器交互（建议机密存储）
- UI：侧栏“网络服务”分组新增“Zerotier”，管理启用/加入网络/路由
- Apply：管理 `zerotier-one` 服务，针对 `selfhost` 将客户端指向自建控制器并使用 `apiToken` 完成所需操作；加入/离开网络，按配置设置路由；nftables 放行必要流量；禁用时停止服务并撤销路由

