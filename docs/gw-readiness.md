# gw 硬件验收前置报告

**结论：首版 IPv4 单 LAN 基本网关的软件前置门槛已达到，可提交用户审核。** 实体 gw 未连接、未部署、未执行测试。用户审核后再决定是否进入硬件验收；本结论不代表硬件已通过，也不代表全部家用路由扩展功能已完成。

## 最终候选

- 分支：`feat/20261028-home-router`；基线：`083a0f87b386af3de0fceab9915f0861b482bf28`。候选通过独立分支提交评审；原工作区用户改动未覆盖。合并状态以对应 PR 为准。
- 代码/模块/Web/测试源文件集合 SHA-256：`1785012018c578df82a1bf178bcac1e1ca71f0f007f3d4a5263de1def7aab9d3`。
- 已测试 Nix 包：`/nix/store/jsn5a79w3s2px380z5c5wzpbknm5bp6x-routerd-0.1.0`。
- Nix 二进制 SHA-256：`ae99ce0f067e8acf8c7af4f8d5a759c852948028017687fcf5285bdd49c5c19e`。
- 当前代码逐文件与实际 Nix 构建源码快照核对，**零差异**。最终工程文档独立补齐，不改变已测试代码。
- 本次验证的本机制品目录为 `/tmp/nixos-router-pr-candidate/`，包含 `manifest.json`、`source.tar.gz`、`SHA256SUMS`。这些是开发机本地证据，未上传至仓库或 GitHub，不能作为远端下载链接；其他环境请按 [测试命令](testing.md) 复验。Nix 二进制有 Nix store 运行依赖，不能仅复制一个文件就视为目标机已部署；后续应按审核方案构建/传送系统闭包。

## 验证矩阵

| 门槛 | 最终结果 | 证据 |
|---|---|---|
| Go 全量与竞态 | `go test ./...`、`go test -race ./...`、`go vet ./...` 通过；本轮测试命中有效缓存，之前全量race服务包426.187秒 | 本机制品 `go-race.log` |
| 浏览器整体回归 | 30/30通过，0失败，62.12秒 | 本机制品 `browser.log` |
| Nix 模块 | managed 8类冲突、真实SSH端口与显式ListenAddress接线、旧模块4种管理器互斥通过 | 本机制品 `module-managed.json`、`module-apply.json` |
| API驱动有线网络 | QEMU TCG四节点完整通过，172.30秒 | 本机制品 `vm-build.log` |
| WPA2软件无线链路 | mac80211_hwsim完整通过，157.55秒 | 同一统一VM日志 |
| 独立审查 | 前轮及本轮已确认问题均修复；本轮新增显式SSH端口、轮询恢复、健康失败只读回归，无遗留阻塞项 | 本文及设计记录，源码/回归用例可核对 |
| 本地部署 | 非特权generate-only镜像更新；登录、鉴权、资源一致性、重启和配置不变通过 | 本机制品 `local-deployment.json` |
| 实体gw | **未执行，待用户决定** | 不以VM或容器证据代替硬件验收 |

有线测试的 router 未预设 LAN 桥、地址、DHCP 或 NAT，均由 routerd 建立；LAN 客户端通过真实 HTTP API 修改配置并观察结果。覆盖：DHCP/DNS/NAT、DNAT增删、MAC阻断与其他客户端正常、NAT关闭后的实际源地址变化、DNS切换、动态WAN DNS、DHCP/静态WAN往返、旧DHCP回调隔离、服务启动失败恢复、未支持能力拒绝、IPv4/IPv6管理面隔离、关闭可选防火墙时真实SSH多端口及显式ListenAddress双栈隔离、重复应用、激活中途强杀后的额外LAN成员清理、坏草稿恢复、进程/整机重启、真实LAN改址及新地址登录。

无线测试使用同一隔离内核的两个虚拟无线电及 station 网络命名空间，验证 WPA2/CCMP 真实关联、错误密码失败、实际 dnsmasq 租约与连通性、API关闭断连、重开、未应用草稿不随服务重启生效、整机重启后恢复。它不证明MT7927驱动、真实射频、吞吐或法规适配。

浏览器中的能力提示拦截用例仅作为界面证据。后端模拟runner用例验证逻辑和故障分支，不冒充真实网络执行。

## 已落实的设计边界

- 独立网络管理器与 NetworkManager/networkd/dhcpcd/ConnMan 互斥；专属协议服务及自己的 nft 表，接口必须显式授权。
- 保存草稿、配置应用、WAN取得租约、客户端连通是不同状态。API先返回作业编号再变更网络；状态/阻断信息使用已应用快照。
- 普通失败恢复last-good；坏草稿不阻止启动恢复且保留原文件；损坏last-good拒绝接管。进程持有状态目录独占锁。
- 不增加LAN改址超时自动回滚。已有last-good时拒绝桥名和WAN物理接口迁移，避免未经验证的资源迁移。
- dnsmasq启动握手确认初始化；hostapd必须报告ENABLED。动态WAN DNS文件保留私有目录权限，专属dnsmasq使用root读取，未对外放开状态目录。
- SSH管理防护取自NixOS实际监听端口，不依赖Web草稿。SSH与系统身份由NixOS管理。
- generation只保留当前及两份历史；预检失败候选立即删除。目录/敏感配置为0700/0600。
- 不支持PPPoE、IPv6路由、QoS、DDNS、家长控制、多AP/访客、UPnP、插件运行应用；managed后端拒绝启用。5GHz仅支持列明的非DFS信道，仍受国家/驱动限制。
- 首次创建桥与打归属标记之间若被强杀，会安全拒绝无归属桥；需独立控制台人工核实，不自动收编未知桥。

## 提交前复审（2026-10-28）

- 修复遗漏 `services.openssh.listenAddresses[*].port` 的隔离规则；22、2223、显式2224均通过 IPv4/IPv6 LAN允许、WAN拒绝测试。
- 修复总览在隐藏标签/忙碌时跳过一次轮询后永久停止的问题；离开页面会取消轮询。
- 健康接口临时失败保留已确认的宿主管理能力；初次能力未知时SSH/System只读，确认generate-only后允许编辑。
- 新增3条浏览器回归先失败后通过，模块新断言先失败后通过。新增SSH测试一度生成非法IPv6监听地址，已用真实sshd确认根因并修正测试为`[::]`，最终两套VM完整通过。
- CI新增两个managed VM及模块断言，保留旧generate-only VM。远端CI结果以对应PR为准；本地无KVM未重跑旧VM。
- 源码集合摘要为按路径排序的 `SOURCE-SHA256SUMS` 文件的SHA-256；工程文档和CI文件随源码包保存，但不纳入该运行源码摘要。

## 用户审核入口

本地预览：<http://127.0.0.1:18080>，使用现有管理员账号和密码。预览仍为generate-only，不能操作宿主网络。更新前后及重启后的配置摘要一致：`643b7b08ec2e129d4cbf48eee4d040c6f7f6e8e6969bbb965070d3996c960175`；旧容器和状态备份已保留，位置见部署验证记录。

- [设计及审查契约](superpowers/specs/2026-10-28-gw-readiness-design.md)
- [任务与验证记录](superpowers/plans/2026-10-28-gw-readiness.md)
- [可复现测试命令](testing.md)
- [待审核的实体硬件验收方案](gw-hardware-acceptance.md)，含本地gw配置快照、Kea迁移、WiFi双网段转单LAN影响、独立控制通道与人工恢复顺序。
