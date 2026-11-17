# gw 集成软件候选功能对照表

核对日期：2026-11-12。近期目标见 [目标与完成条件](superpowers/specs/2026-11-12-gw-integration-goal.md)。

需求基线：本地 `nixos-home` HEAD `c7e1bbd333bd76953fb77d9e01449fff06269077` 的 `mesh/hosts/gw/{router,configuration,modules,default}.nix`，以及引用的 `modules/system/{network/default,vlan}.nix`。工作区的 flake/锁文件存在用户修改，有效配置评估包含这些修改；HEAD 单独不能复现该评估。这是配置源码与 Nix 评估核查，尚未证明实体 gw 当前运行状态；集成时须固定全部候选输入并核对目标设备。

实现基线：`feat/20261109-managed-ipv6`，HEAD `4d0e9c1` 加当前未提交改动。已有通过记录不覆盖后续修改；最终交付必须固定候选提交并重新核验适用检查。

## 本轮必需项

| ID | gw 所需行为及来源 | 当前通用框架状态 | 候选验收证据要求 |
|---|---|---|---|
| G01 | 物理 WAN 使用 DHCP；有线 LAN 使用桥连接多个端口（router.nix） | 已实现；最新有线 VM 有通过记录 | API 应用后真实取得 WAN 地址、建立授权桥/成员及默认路由，客户端可联网；禁止接管未授权接口 |
| G02 | 有线 LAN 与 WiFi 保持两个独立 IPv4 网段，分别提供地址池与网关（router.nix） | 独立 WiFi IPv4 模型、页面、生命周期与完整 hwsim VM 已通过；双网段 IPv6 另见 G07 | 有线与无线客户端取得各自网段地址、网关和 DNS；两侧均能通过正确出口联网；管理/跨网段策略有明确矩阵；重复应用、关闭、失败与重启不残留资源 |
| G03 | 两个网段的 DHCP 租约、域名及指定 DNS 服务器（router.nix） | 两侧真实 DHCP/网关/DNS 与指定上游已通过双网段 VM；固定候选仍需复验 | 各客户端实际获取并使用预期选项；动态 WAN DNS、显式上游和 IPv6 DNS 来源不互相覆盖 |
| G04 | 两个内网的 IPv4 NAT 与必要转发（router.nix） | 双网段 NAT 与明确管理/互访策略已通过真实流量 VM，含关闭 WAN 过滤仍隔离 | 两侧实际出站与回程、NAT 源地址观察、WAN 未请求入站拒绝；停用后精确撤销；明确跨网段策略 |
| G05 | 5 GHz AP、WPA3-SAE、外部密码文件（router.nix） | 纯 WPA3-SAE/强制 PMF、页面选择/秘密保留和真实 SAE 软件 VM 已通过；设备秘密及射频验收由集成方完成 | 通用认证模型、严格校验、私有凭据与生成配置；真实 WPA3 关联/错误密码拒绝、DHCP/流量、停止和恢复；不得在日志/Nix store 中暴露密码 |
| G06 | WiFi 6/7、80 MHz、对应信道与监管域配置（router.nix） | 标准/20–80MHz契约、真实PHY预检、渲染与页面已实现；WiFi6/7各自80MHz完整hwsim生命周期及WiFi7+双网IPv6组合已通过；最终固定候选仍需适用复验 | 按实际无线能力校验模式/带宽/信道组合，生成并验证 hostapd 配置；不支持时明确拒绝。MT7927、CN 监管域、实际射频/吞吐留待硬件验收 |
| G07 | IPv6 可用；WAN RA/SLAAC，候选补齐可路由 LAN 所需的 DHCPv6/PD（router.nix 与候选目标） | 单 LAN 协议/生命周期/MTU/PMTU VM 已通过；双网段 PD 的资源/内核接线已通过，完整协议 VM 已通过（含变更/容量不足/过期/重启/关闭） | 支持组合逐项通过真实协议/流量；前缀更新/失效与关闭清理、DNS、ICMPv6/PMTU、重启恢复；双网段 PD 分配互不重叠；目标内核支持所需作用域转发能力 |
| G08 | 管理访问规则、宿主 SSH、设备身份与系统设置（已确认设计及 gw modules.nix） | root 执行器/低权限 API、独立无线管理授权及撤销已通过，默认无线管理关闭 | 明确哪些网段可管理；IPv4/IPv6 正反例一致；SSH 实际端口受保护。SSH/系统身份由 nixos-home 配置 |
| G09 | 独立网络管理器与原有网络服务迁移（已确认设计） | 已有配置期/启动互斥；新增 IPv6 地址/Kea6/radvd 冲突评估已通过 | 集成时显式移除原桥/地址、Kea、hostapd、NAT/防火墙及冲突 DHCP 客户端；不得由程序自动停止其他管理器；先系统构建验证，实际切换待用户决定 |
| G10 | 保存、应用、失败恢复及整机重启后的持续运行（候选目标） | 单 LAN IPv6 与双网段 IPv4 的失败/重启恢复 VM 已通过；双网段 IPv6 完整生命周期已通过；WiFi6/7完整生命周期与WiFi7+双IPv6组合已通过；PPPoE6完整协议/生命周期VM已通过 | 固定候选执行真实流量及故障注入；保留 last-good、原始作业 ID 和版本边界；LAN 改址无新增倒计时自动回滚 |
| G11 | MT7927 驱动、内核与 AP 准备步骤（configuration.nix/router.nix） | 集成方责任；不能把现有 hostapd-prep 删除步骤直接迁入通用执行器 | nixos-home 保留驱动/固件/监管域及授权的设备准备；校验目标内核和依赖；模拟无线结果不替代实体设备测试 |
| G12 | Web/API 配置与运行能力一致（候选目标） | managed-router 能力声明已包含独立无线网段和 Ethernet IPv6 模式；WPA3、第二网段、无线标准/带宽页面已接通，48项浏览器回归通过；PPPoE6 完整协议验收后已同步能力声明，定向契约测试通过 | 所需配置可编辑、保存、应用与回读；错误保留输入并定位字段；未支持能力清楚标明；双份 Web 资源同步并通过浏览器回归 |
| G13 | 版本交付与 nixos-home 系统构建（候选目标） | 当前工作分支含大量未提交修改，尚未交付 | 审查与修复完成、提交/PR、固定依赖版本、gw 系统评估及构建成功；候选清单绑定源码、检查及产物；提供迁移/人工恢复和硬件验收步骤 |
| G14 | ZeroTier 服务与现有 UDP 9993 入口（有效配置评估） | 已接入可信 `managed.wanUDPPorts`，双栈/PPP 隔离报文测试通过；完整 WAN UDP VM 已通过；MTU 实现后的生命周期 VM 也通过 | 显式、受限的宿主服务放行配置及 UDP 正反例；保留 overlay 联网所需入口，overlay 到管理面/内网另行授权，不能自动赋予受信 LAN 身份 |

## 有效配置评估（2026-11-12）

使用 `builtins.getFlake "git+file:///data/workspace/repos/nixos-home"` 只读评估 `nixosConfigurations.gw.config`；结果保存在开发机 `/tmp/nixos-home-gw-effective-network.json`，日志 `/tmp/nixos-home-gw-effective-network.log`。没有修改或激活该配置。

- 防火墙启用，`filterForward=false`，受信接口只有 `lo`，TCP 放行 22，UDP 放行 9993。该基线没有声明两个家庭内网之间的转发隔离；候选须显式表达互访策略，管理端口遵循已批准的 LAN-only 边界。
- NAT 对有线桥和独立 WiFi 接口启用，WAN 采用 DHCP。`dhcpcd=true`，NetworkManager 与 networkd 均关闭；接入独立管理器时 dhcpcd 必须显式关闭。
- ZeroTier 启用，Tailscale 关闭。UDP 9993 所需入口及 overlay 路由需纳入迁移检查，不因协议服务仍 active 就判定联网可用。
- 此工作区评估得到内核版本 `7.2`；这不是实体 gw 正在运行的版本。最终构建及硬件预检须确认 IPv6 转发能力和 MT7927 驱动组合。
- 最初采用 `path:` 评估被未跟踪的 `.codegraph/daemon.sock` 阻断；切换到 Git 文件集后成功，未删除 socket 或修改用户文件。

## 集成责任及待确认的访问契约

- 网口、地址、地址池、域名、监管域、信道、驱动与秘密位置均由 nixos-home 配置；上表不将设备示例参数写入通用默认值。
- 原配置的两个网段不能自动合并；WPA3、WiFi 6/7 不能静默降级为现有 WPA2 实现。
- 两个网段的管理与互访规则须从已确认的信任边界和最终有效配置形成明确矩阵。独立网段本身不证明二层/三层隔离；“访客”也不能仅由 WiFi 接口名称推断。
- gw 引用 overlay 模块；其中 ZeroTier 默认启用、Tailscale 在 gw 显式关闭。其最终有效状态与所需路由在系统评估时确认；overlay 不自动成为受信管理网段。外部 overlay 服务由 nixos-home 管理，受管规则须有访问正反例。
- 配置源码中的 IPv6 NAT 开关不自动成为必须引入 NAT66 的要求。候选优先采用可路由委派前缀，并以客户端实际 IPv6 流量验收；若运营商限制影响目标行为，须明确记录。

## 已有证据与当前限制

- `/tmp/router-protocol-regression.log`：最新有线及 IPv4 PPPoE VM 分别完成于 210.20s、105.60s。
- `/tmp/router-ipv6-vm-endpoint.log`：基础真实 IPv6 VM 83.68s 通过；`/tmp/router-ipv6-vm-lease-loss.log`：失联/自然到期/恢复扩展 VM 219.57s 通过。
- `/tmp/router-ipv6-ownership-green2.json`：模块冲突及兼容评估全部为真；`/tmp/router-ipv6-bridge-forward.log`：真实网桥隔离转发测试通过。
- IPv6 重启测试 `/tmp/router-ipv6-vm-reboot-modes.log` 因 QEMU 未以 `allow_reboot=True` 启动而失败。修正后 `/tmp/router-ipv6-vm-reboot-enabled.log` 完整脚本 257.01s 通过，包含重启恢复、SLAAC/无 PD、IPv6 LAN DNS、失联恢复与关闭；制品 `/tmp/router-ipv6-vm-reboot-enabled`。此快照早于 WAN UDP 清单改动。
- WAN UDP 清单：`/tmp/router-wan-udp-full-go.log`、`/tmp/router-wan-udp-vet.log`、`/tmp/router-wan-udp-race.log` 通过；`/tmp/router-wan-udp-kernel.log` 17.094s 通过 IPv4/IPv6 指定端口、邻近拒绝、PPP 物理口及管理保护正反例。最终 Nix 模块评估 `/tmp/router-wan-udp-module-final.json` 全真；完整 VM `/tmp/router-wan-udp-vm.log` 的宿主服务入口子测试 5.47s 通过，完整重跑 `/tmp/router-wan-udp-vm-ready.log` 277.15s 通过；此前首次完整运行因模式切换后 DHCP4 尚未就绪失败，独立有界等待后通过。真实 ZeroTier 服务与外部 overlay 联通仍由集成/硬件阶段验证。
- 这些路径是开发机本地历史证据，不是远端可下载发布制品；最终候选需要复验。本表未声明实体 gw 已部署或验收。

- MTU 新证据：`/tmp/router-mtu-vm.log` 287.79s 完整生命周期通过；真实 MTU 恢复、PTB 报文分别 `/tmp/router-mtu-kernel.log` 与 `/tmp/router-mtu-forwarding.log` 通过。更严格的 ISP RA MTU1400、PTB、大 TCP、IPv4大报文及完整生命周期 VM `/tmp/router-mtu-e2e-vm.log` 295.40s 全部通过；双网段 PD 后续已实现资源接线，完整协议验证见 G07。

- WPA3 软件验收：`/tmp/router-wpa3-vm.log` 162.42s 完整通过，制品 `/tmp/router-wpa3-vm`；45项浏览器回归 `/tmp/router-wpa3-web-full.log` 全部通过。G05 的物理无线能力仍待用户授权后的硬件阶段核验。

## 独立无线网段访问契约与当前证据

| 流量 | 当前策略 |
|---|---|
| 有线 LAN → Web/宿主 SSH | 保持既有允许规则 |
| 独立 WiFi → Web/宿主 SSH | `wifi.network.allowManagement` 显式开启；默认拒绝，撤销先于 established 接受 |
| 独立 WiFi → DHCP/DNS/网关检查 | 仅按启用的 DHCP/DNS 服务及必要 ICMP 规则允许；不因此获得其他宿主服务访问 |
| 有线 LAN ↔ 独立 WiFi | `allowWiredAccess` 显式开启双向发起；默认拒绝，含已有连接撤销 |
| 两侧 → WAN | 各自出站与 established 返回；IPv4 NAT 由既有 `natEnabled` 控制 |
| WAN/外部接口 → 独立 WiFi | 拒绝未经请求的新连接；关闭可选 WAN filter 不移除此边界 |
| overlay → 管理面/独立 WiFi | 不自动成为 LAN；另行配置和验收所需路由/访问 |

双网段 IPv4 完整 VM `/tmp/router-wifi-routed-extended-vm.log` 315.89s 通过，包含 DHCP/DNS/NAT、访问授权撤销、WAN filter 关闭、单次 AP 故障恢复、禁用、重启与桥接切换。对应制品 `/tmp/router-wifi-routed-extended-vm`，尚未固定为交付提交。后续双 IPv6 接线已通过定向/真实内核测试，公开 Apply/Restore 组合已开放且最新全量 Go 通过（`/tmp/router-dual-public-full-go.log`）。完整双网协议 VM 已终态退出0（`/tmp/router-dual-protocol-vm.log`，390.98s），覆盖七项协议/生命周期检查，不能与已通过的 IPv4 VM 混同。

PPPoE6 完整验收：`/tmp/router-pppoe6-vm-ready.log` 286.13s、八个子测试全部通过；制品 `/tmp/router-pppoe6-vm` → `/nix/store/8bdais55fwr6jz9pzz1hglv7gxnva23v-vm-test-run-vm-managed-pppoe6`。真实 IPv6CP、IA_NA/PD、双栈 DNS/流量、MTU/MSS、无 NAT66、自然过期、对端失联、pppd 崩溃、整机重启、关闭 IPv6 和切回 DHCP 均有协议与流量断言。该测试发现并修复 PPP net-init 前置 RA 归属设置；需补 IPv4 PPP 回归及最终固定提交检查。

共享 PPP net-init 修复后的 IPv4 PPP 完整回归 `/tmp/router-ppp-init-ipv4-vm.log` 105.74s 通过。nixos-home 独立副本预检构建已退出0，产物 `/tmp/nixos-home-gw-preliminary-system`，包含 Linux 7.2 与 MT7927 驱动；该产物输入仍是未提交快照，不能替代最终固定提交构建。initrd 静态检查确认包含 init 与 libcrypt.so.2，旧版 libcrypt.so.1 名称警告不是 ELF 直接依赖缺失；未进行实体启动验收。
