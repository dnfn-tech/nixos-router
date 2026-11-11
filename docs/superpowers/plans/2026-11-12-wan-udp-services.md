# WAN UDP 服务入口实施计划

> 使用 superpowers:executing-plans 在当前隔离 worktree 自行实施，完成后纳入候选整体验收与独立审查。

**Goal:** 为 gw 的 ZeroTier 等宿主服务提供显式、受限的 WAN UDP 入口。
**Architecture:** NixOS 的 `managed.wanUDPPorts` 通过可信 root executor 启动参数传入 `managed.Options.WANUDPPorts`，仅在实际 WAN 出口 input 链放行。默认空清单，配置 API 无此权限；不自动信任 overlay 接口、不扩大 forward 或 TCP 管理访问。
**Tech Stack:** Go、NixOS、nftables、隔离网络命名空间的真实 UDP 报文。
**Spec:** `docs/gw-integration-matrix.md` G08/G14；`docs/superpowers/specs/2026-11-12-gw-integration-goal.md`。

## Global Constraints

- 不操作实体 gw 或宿主网络；保留接口授权与管理端口保护。
- 端口仅允许 1..65535，最多 64 项，去重且复制输入；默认不放行任何额外端口。
- 配置来源属于 NixOS 部署权限，不能加入用户可编辑的 Config/JSON/RPC。
- DHCP/静态 WAN 使用物理出口，PPPoE 使用逻辑出口；切换与移除通过已有原子 nft 事务生效。

## Review Focus

- 空值、越界、非数字及过长清单必须在网络变更前拒绝；默认空清单合法。
- 清单放行 IPv4/IPv6 指定 UDP 端口；邻近未授权端口仍拒绝。
- PPP 模式不得在物理 WAN 额外打开该入口。
- 清除清单后停止新入站服务流量；已有 established 流量遵循既有连接跟踪规则。
- TCP Web/SSH 规则、forward 策略及外部 nft 表不被扩大或删除。

## Task 1：可信配置到真实报文

**Files:** `backend/internal/managed/{manager.go,render.go,wan_services_test.go}`、`backend/cmd/router-executor/{main.go,main_test.go}`、`modules/managed.nix`、`tests/{module-managed.nix,ipv6-firewall-packets.py}`、`backend/internal/managed/ipv6_firewall_test.go`、`docs/landing.md`。

**Interfaces:** `Options.WANUDPPorts []int`；CLI `--wan-udp-ports` 逗号分隔；Nix `services.nixos-router.backend.managed.wanUDPPorts`，默认 `[]`。

- [x] 添加构造/CLI 边界测试、实际出口规则及默认关闭用例，运行并观察缺少字段/参数的 RED。
- [x] 在构造阶段校验、去重并复制清单；启动参数传入 Manager；渲染 actual-egress UDP allow，复用现有 nft 原子应用。
- [x] 增加 Nix 参数接线与空默认值评估；非法端口由 Nix/Go 边界拒绝。
- [x] 扩展真实报文测试：服务端口 9993 通过、9994 拒绝；清单为空时 9993 拒绝；PPP 物理口无放行；保留 TCP 管理拒绝与外部表。
- [x] 运行全量 Go/vet、针对性 race、模块评估及 opt-in nft 命名空间测试，结果必须明确终态。
- [ ] 更新集成文档与 G14 证据，按候选审查结果提交；不得将单项通过视为整个 gw 候选完成。

## 验证记录

- `/tmp/router-wan-udp-red.log` 缺字段/参数 RED 后，定向 GREEN；额外 65 个合法端口与 CLI 越界用例通过 `/tmp/router-wan-udp-boundary.log`。
- 全量 Go、vet、managed/executor race 通过；日志分别为 `/tmp/router-wan-udp-full-go.log`、`/tmp/router-wan-udp-vet.log`、`/tmp/router-wan-udp-race.log`。
- 隔离真实报文 `/tmp/router-wan-udp-kernel.log` 17.094s 通过；原模块评估 `/tmp/router-wan-udp-module.json` 全真。
- 锁定 Nix `types.port` 为 u16，包含 0；新增配置期拒绝 0 用例在 `/tmp/router-wan-udp-zero-red.log` 观察 RED。补断言后完整模块 43552 运行中，结果 `/tmp/router-wan-udp-module-final.json`。
- 完整双栈 VM 加入真实两端口监听服务、LAN 正向对照、WAN 9993 通过及 9994 拒绝，运行 47081，日志 `/tmp/router-wan-udp-vm.log`，输出 `/tmp/router-wan-udp-vm`，尚未计通过。

- 43552 最终模块评估退出0，/tmp/router-wan-udp-module-final.json全部true。完整VM47081的实际宿主UDP入口子测试5.47s通过，含双栈LAN正向对照与WAN端口正反例；全套生命周期回归仍在运行，尚未计整套通过。

- 完整 VM47081 终态退出1：SLAAC 模式切换后 IPv6 先恢复，IPv4 DHCP 尚在 DISCOVER 阶段；将重启/切换后的 IPv4 连通性独立改为有界等待，保留仅 DHCP6 失联时的即时 IPv4 连通断言。重跑70370终态退出0，完整脚本277.15s，日志 `/tmp/router-wan-udp-vm-ready.log`，制品 `/tmp/router-wan-udp-vm-ready`。该快照包含 WAN UDP，不包含后续 MTU 修改。
