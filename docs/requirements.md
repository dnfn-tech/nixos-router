# nixos-router WebUI 需求说明（对齐当前原型）

本文件为家用路由器 Web 管理界面的产品需求与信息架构说明，作为 `nixosModules.default` 模块的对外文档。内容与交互原型（uploads/index.html）及《DESIGN-BRIEF》一致，并与存储选型文档保持一致性。

- 单一真源（SoT）：`/var/lib/nixos-router/config.json`（意图配置）
- 运行态与附加数据：`/var/lib/nixos-router/state.db`（SQLite，包含 sessions / audit / jobs / 可选 revisions）
- NixOS 模块安装 Web UI + API + apply agent，监听 LAN，仅本机重启/激活时会从 JSON 重新应用。
- 通过远程/git 更新程序与模块时，绝不清空 `/var/lib/nixos-router/`。
- 存储选型细节见 `docs/storage-choice.md`（已有开放 PR 提交）。

## 目标与非目标
- 目标
  - 在局域网浏览器中配置并“立即应用”网络与系统功能；无需以远程 `nixos-rebuild` 作为日常运维路径。
  - 保持配置可读、可备份、可回滚，设备离线也能自洽。
  - UI 简洁，默认安全（WAN 入站拒绝、LAN 仅必要端口放行）。
- 非目标（本期不做）
  - 企业/数据中心级别的复杂路由/ACL/多租户。
  - Mesh 控制器、全面 VPN 门户、完整流量大盘等高复杂功能。

## 存储模型
- `config.json`：人类可读的意图配置，覆盖 WAN、LAN、WiFi、DNS、防火墙、端口转发、UPnP、设备、家长控制、QoS、DDNS、IPv6、SSH、系统与备份策略等模块。
- `state.db`（SQLite）：用于高频/追加型的运行态数据与审计，包含：
  - 会话与登录状态（sessions）
  - 审计日志（谁在何时做了什么、结果如何）
  - apply 作业队列与状态（jobs）
  - 可选：配置修订索引（revisions，指向历史 `config.json` 快照标识）
- 敏感字段（PPPoE、WiFi、DDNS Token 等）仅保存在设备本地，不进入 git；备份时明确标注“包含敏感信息”的完整包。
- 详细 rationale 参考 `docs/storage-choice.md`（混合：JSON + SQLite）。

## 信息架构（侧栏）
- 基础：概览、上网（WAN）、局域网（LAN）、WiFi、设备
- 网络服务：DNS、防火墙、QoS、家长控制、动态 DNS、IPv6、广告过滤（AdBlock，可选）、流量统计（Traffic，可选）、代理（Mihomo，可选）、VLAN（可选）、Tailscale（可选）、Zerotier（可选）
- 系统：远程管理（SSH）、系统、插件（模块管理）

说明：概览为状态卡片与“扁平网口视图”（WAN/LAN1… 并列，无拓扑；不展示无线拓扑）。

## 功能清单（与原型一致）
- 登录（LAN-only 管理）
- WAN：DHCP / 静态 / PPPoE
  - PPPoE：账号/密码、可选 Service/AC、是否使用运营商 DNS；NAT 经 `ppp0`；断线重拨由 agent 管理
- LAN：单一逻辑 LAN（`br-lan` + 多物理口桥接 `lan.ports[]`）
  - DHCP 池（起止、租约时长、内网域名）
  - 静态绑定（MAC → 固定 IP），支持池外优先
- DNS：上游服务器列表、内网域名（search/domain）
- 防火墙：WAN 入站默认拒绝；LAN 服务端口放行；端口转发（DNAT）；UPnP（开关与映射查看）
- WiFi：与有线同一逻辑 LAN（无线 AP 接口桥接入 `br-lan`，同网段/同 DHCP/DNS）；能力驱动的并发模型：根据硬件 `nl80211` 能力自动裁剪（如仅支持 `AP≤1` 则单 AP；若支持 DBDC/多 AP 则可并发 2.4G+5G）；访客 WiFi 默认通过 BSS（多 SSID）与客户端隔离/防火墙阻断访问 `br-lan`；无无线硬件时整页禁用
- 设备：在线列表、改名、拉黑、跳转至限速/静态绑定
- 家长控制：按设备/组的周历时段规则；“立即断网/放行”动作
- QoS：全局上下行带宽；按设备优先级/限速（示例：cake/fq_codel）
- DDNS：Cloudflare / DuckDNS / 阿里云 / 自定义（模板 URL）
- IPv6：WAN DHCPv6/SLAAC/PPPoE+IPv6；LAN PD/RA；必要 ICMPv6 放行
- SSH：启用、密码/root 登录开关、wheel 免密 sudo
- 系统：修改密码、备份/恢复（`config.json` 或完整包 `config.json+state.db`）、审计、重启
- DHCP/DNS 后端：以 dnsmasq 为主（提供 DHCP 池、静态绑定、内网域名与上游转发）；不引入 Kea 作为 v1 路径
- 广告过滤（可选 `plugins.adblock`）：基于 dnsmasq 的 blocklist（`hosts`/`address=/` 生成）；后续可选 AdGuard Home；支持订阅、刷新周期、（later）统计
- 流量统计（可选 `plugins.traffic`）：接口/客户端流量展示；首选 vnstat（或 nft/conntrack 聚合）；只读统计 API 与启用/保留期配置
- 代理（可选 `plugins.mihomo`）：基于 Clash Meta（mihomo），生成配置与管理服务；可选 TUN；与 dnsmasq 的 DNS 交互在 `redir-host`/`fake-ip` 模式下需谨慎；禁用隐藏页面并在 apply 停止服务
- VLAN（可选 `plugins.vlan`）：提供多 VLAN/LAN 能力（802.1Q VID、每 VLAN 桥/子网/DHCP、隔离），默认插件关闭时保持单一 `br-lan`；启用后可管理 VLAN；（later）WiFi BSS → VLAN 映射
- Tailscale（可选 `plugins.tailscale`）：远程接入/overlay；支持三种控制面模式：\n  - `official`：tailscale.com 官方控制面\n  - `selfhost`：连接外部 Headscale（`loginServer`）\n  - `headscale`：在本机运行 Headscale（管理 `headscale.service`，数据于状态目录，生成预授权密钥，本机客户端使用本地 `--login-server`）；谨慎配置防火墙，仅对 LAN 放行管理端口；禁用隐藏页面并在 apply 停止 `tailscaled`/`headscale` 并撤销路由
- Zerotier（可选 `plugins.zerotier`）：远程接入/overlay；支持 `controlPlane: official | selfhost` 与 `controllerUrl`/`apiToken`；加入 `networks[]` 并按需配置路由；谨慎配置防火墙；禁用隐藏页面并在 apply 停止服务与撤销路由

## 保存/应用 UX 状态机
- 状态
  - Clean：无本地更改
  - Dirty：表单有未保存更改
  - Validating：客户端/服务器端校验
  - Applying：写入 `config.json.tmp` → 原子替换 → 触发 apply 管道
  - Succeeded：应用成功，清空 Dirty，写审计日志
  - Failed：应用失败，展示错误，保持/回滚至上一次已知良好版本
- 行为
  - 离开页面时如 Dirty → 二次确认
  - 并发：单设备上串行 apply；后发请求可排队或取消前一作业
  - 失败策略：不破坏当前已运行配置；错误细节可从作业日志下载

## 安全
- 管理界面仅在 LAN 监听（默认 0.0.0.0:8080，仅 LAN 可达）；防火墙自动放行该端口的 LAN 流量。
- 会话基于 Cookie（HttpOnly、SameSite=Lax/Strict）+ CSRF 保护（双提交/同源策略）；密码散列使用 argon2id/bcrypt。
- 审计默认开启：登录、配置变更、apply 成败、下载/恢复操作均写入。
- 不提供 WAN 侧管理入口；IPv6 同样默认不开放。

## 非功能性要求
- 可扩展性：后端采用“稳定核心 + 模块/插件”架构。核心提供配置注册、作业队列、审计、会话与机密存取等通用能力；功能以模块化实现（WAN/LAN/WiFi/Firewall/QoS/Parental/DDNS/IPv6 等作为内置模块），并预留第三方插件扩展（`config.json` 中 `plugins.<id>` 命名空间、统一 apply 钩子）。首版可通过重启服务完成启用/禁用（冷加载），后续再演进热加载。
- 能力驱动：WiFi 页面/后端根据无线芯片 `nl80211` 接口组合能力裁剪选项（是否单 AP、是否并发 2.4+5G、是否支持 BSS 访客），禁止生成硬件不支持的配置。
- 插件驱动页面/导航：导航与页面由模块/插件的 Manifest 声明（`ui.nav`/`ui.pages`），后端统一汇总（`GET /api/v1/ui/nav` 或 `GET /api/v1/plugins`）；禁用插件即隐藏对应导航/页面且路由返回 404/disabled；核心页面固定存在，不支持运行期禁用。

## 里程碑（建议分期）
1) 基础 schema 与只读 UI
   - 定义 `config.json` 结构与验证；读取并展示当前状态（含“不可用”占位）
2) 可写核心面（WAN/LAN/DHCP+静态、DNS、防火墙+端口转发、WiFi、DDNS）
   - 完成最小 apply 管道与作业追踪
3) 认证与审计完善
   - 会话管理、CSRF、密码策略、审计检索与导出
4) 高级服务
   - 家长控制、QoS、UPnP、IPv6 细节打磨与稳定性
5) 系统级操作
   - 备份/恢复、日志查看、重启、故障自愈与回滚

## 后续（later）
- 多 WAN、策略路由与故障转移
- 多 LAN / VLAN：通过可选 `vlan` 插件演进（非核心 v1 默认），与访客隔离/更细粒度策略在后续完善
- Mesh / 无线漫游优化
- VPN（客户端/服务端）
- USB 网络共享
- 流量报表与长期统计
- 广告过滤与家长内容分级
 
## 验收（插件页面控制）
- 关闭可选插件（如 QoS/DDNS/IPv6/AdBlock/Traffic/Mihomo/VLAN/Tailscale/Zerotier）后，其侧栏入口与页面不可见；相关 API 返回 404/disabled
- 重新启用后，入口与页面恢复；核心页面（overview/wan/lan/wifi/clients/dns/firewall/ssh/system/plugins-mgmt）始终存在且不可通过 WebUI 禁用

## 参考
- 设计摘要：《DESIGN-BRIEF》
- 交互原型：`docs/prototypes/index.html`（可选随仓库提供）
- 存储选型：`docs/storage-choice.md`（开放 PR 已提供）
- 目标主机说明：`docs/target-host-notes.md`（首批对象机型迁移方向）

