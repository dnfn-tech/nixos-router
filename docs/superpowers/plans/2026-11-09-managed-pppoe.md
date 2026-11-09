# Managed PPPoE Implementation Plan

> 使用 superpowers:executing-plans，自行实施已批准完整设计中的 PPPoE；最终一次独立审阅。

**Goal:** 专属 pppd 实现 WAN 拨号、断线重拨、实际出口 NAT/DNAT、DNS 与失败可观测性。
**Architecture:** 物理 WAN 仅用于 PPPoE，固定专属 PPP 接口 `nr-ppp0`；凭据仅在 root 私有 generation 选项文件；systemd 管理独立 pppd，停用时释放接口/路由。保留既有配置事务和失败恢复。
**Tech Stack:** Go、NixOS/systemd、ppp 2.5、nftables、三节点 VM 模拟接入服务器。
**Spec:** ../specs/2026-10-15-usable-home-router-design.md 第5节和A05/A07/A08/A10。

## Global Constraints

不操作实体 gw、宿主网络、不合并PR。实际账号/网口由 nixos-home 提供。与通用网络管理器互斥。不增加改址倒计时回滚。所有功能必须在 VM 真实协议/流量验证后才宣称可用。

## Review Focus

- 密码中的引号、反斜杠、换行不能注入 pppd 参数，不进入命令行/日志/Nix store。
- 外部占用 nr-ppp0 时拒绝；不得借固定名称接管其他接口。
- DHCP/static/PPPoE 切换和断线重连不留下旧出口 NAT、路由、DNS；旧 generation 回调不得污染新配置。
- 认证失败和上游不可达不能伪装联网；配置已应用和连接已建立分别报告。
- 关停、进程崩溃、重启恢复均保留 LAN 管理通道；不会无限等待阻塞全部 API。

## Task 1: 受限选项与出口契约

Files: 新建 backend/internal/managed/pppoe.go、pppoe_test.go。
Interfaces: managedPPPInterface 常量 nr-ppp0；wanEgress(config.Config) string；renderPPPoE(config.Config) (string,error)。输出不包含 hook 路径（由 Nix 固定注入），不运行命令。
- [ ] RED：安全引用密码（空格、#、双引号、反斜杠）、拒绝控制字符/缺失账号、固定接口/MTU1492/重拨选项、DHCP/static出口保持原接口。
- [ ] 实现纯渲染辅助函数；非PPPoE返回空字符串；当前 managed 校验仍拒绝PPPoE，直至Task2完整接线。
- [ ] go test ./internal/managed、vet；本地提交。

## Task 2: 专属服务与运行链路

Files: manager.go/render.go、modules/managed.nix、status/API/Web能力、tests/module-managed.nix。
- [ ] RED：所有权冲突拒绝、模式切换stop/start序列、私有文件权限、PPP出口NAT/DNAT、候选回调generation校验、无地址与观察错误的状态。
- [ ] 接入渲染和固定出口，防火墙同时保护物理和PPP接口；MSS按实际出口MTU限制。启动专属pppd，守护断线重连，禁用时停止。凭据仅通过0600文件传递。Nix固定插件/钩子/设备权限。
- [ ] 回调更新专属WAN resolver，不操作全局resolv.conf；标记generation，旧回调拒绝；读取运行态地址+服务状态，连接等待/认证失败不报联网成功。
- [ ] 全量Go/race、模块eval、浏览器真实API及能力回归；具体字段和状态协议随代码定位后在ledger记录。

## Task 3: 真实PPP隔离验收与提交

Files: 新建 tests/managed-pppoe.py、对应Nix VM定义与flake/CI接线；更新testing/landing/status。
- [ ] 模拟PPPoE上游、router、LAN client：正确凭据真实拨号/NAT/DNS/DNAT、错误密码、上游断开重连、模式切换、关闭清理、重启恢复。
- [ ] WAN管理访问拒绝、LAN可用；凭据不在ps、普通用户不可读文件；PPP服务重启后真实客户端流量恢复。
- [ ] 新PPP VM + 既有有线/WiFi/privsep/legacy VM回归；独立整变更审阅一次，Important按RED→GREEN修复。
- [ ] 文档准确区分本地测试、CI、PR、硬件验收；不因缺少真实gw配置缩小通用功能目标。

## Rulings / execution ledger

- 使用现有完整设计的专属 pppd 方案，无新增产品范围，无需再次批准。工程选择：固定 nr-ppp0、MTU/MRU1492、LCP保活10秒/3次、重试间隔5秒；这些默认值仍须VM证据。
- 2026-11-09 基线0ca8e8b，分支feat/20261109-managed-pppoe。当前源码在validate中拒绝PPPoE，所有nft出口使用物理WAN；不能仅移除拒绝就宣称支持。
- 核对本地ppp2.5.2手册：ifname冲突会退出，dryrun会输出参数，因此生产预检不使用dump/dryrun泄露配置。官方参考 https://ppp.samba.org/pppd.html 。

- Task 1 本地验证完成：TestPPPoEOptionsAndEgress / TestPPPoERejectsUnsafeOptions 先因缺少实现RED，再通过完整 managed 包测试（2.633s，/tmp/router-pppoe-render-tests.log）；vet/diff --check退出0。实现只新增纯函数和测试，尚未接入生产运行入口，managed PPPoE拒绝仍保持。
- Task 1: Ruling: 生成nodefaultroute，由后续受generation保护的ip-up钩子添加默认路由；DNS也由专属钩子写私有resolver。后续必须核验pppd usepeerdns会写/etc/ppp/resolv.conf的副作用，通过服务文件系统隔离避免触碰宿主PPP配置。代价：Task2钩子和隔离未完成前此生成器不构成可拨号实现。
- Task 1: Ruling: 凭据1-255字节、有效UTF-8、拒绝控制字符；符合pppd常见凭据长度边界，错误不回显原值。未在真实pppd解析器/ISP验证，Task3须使用带特殊字符的测试凭据证明正确，而不是只依赖字符串断言。
