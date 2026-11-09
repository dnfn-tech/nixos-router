# 实现现状与验收缺口

核对日期：2026-11-09。源码基线：`2b301dabc636a03d58e764ca173d4af1afcffe14`（PR #16）。本文描述该版本的能力与证据范围，不等同于最新 main、运行中的本地预览或实体 gw 状态。

当前具备显式启用的 IPv4 单 LAN managed 网关实现；**完整家用路由目标仍未完成**。基础候选的通过不能替代完整设计的 A01–A14 验收。完整范围见 [已确认设计](superpowers/specs/2026-10-15-usable-home-router-design.md)，首轮硬件候选范围见 [候选设计](superpowers/specs/2026-10-28-gw-readiness-design.md)。

本仓库负责通用模块、运行引擎、API、Web 和隔离测试。实际网口、地址、无线国家/信道、密钥和迁移安排在 nixos-home 集成时配置；这些专用配置不列为本仓库缺陷。

## 运行模式

| 模式 | 含义 | 不能据此宣称 |
|---|---|---|
| 默认 generate-only | 保存配置、生成文件，适合非特权 Web/API 预览 | 已修改网卡、服务或客户端网络 |
| 旧运行开关 | `applyReload` / `consumeGenerated` / `privilegedApply` 控制旧片段消费路径 | 等同于 managed 的独立网络生命周期 |
| 显式 managed | routerd 管理授权接口及专属协议服务，与其他通用网络管理器互斥 | 所有 schema 字段和插件均支持运行 |

接入和所有权要求见 [接入指南](landing.md)。保存草稿与应用分离；managed 的应用以异步作业最终结果为准，WAN 等待 DHCP 租约也不等于互联网可达。

## 当前实现与证据入口

下表列出实现和测试覆盖入口。测试文件存在不表示某个提交已通过；集成时必须核对所选 SHA 的 CI 结果，并按 [测试说明](testing.md) 复验。

| 范围 | 当前实现 | 验证入口及边界 |
|---|---|---|
| 接口所有权 | 有线/WiFi 显式授权；配置期和运行前拒绝冲突；未知桥拒绝接管 | `tests/module-managed.nix`、`tests/module-apply.nix`、`backend/internal/managed/manager_test.go` |
| IPv4 WAN/LAN | DHCP/静态 WAN、单桥、多有线成员、DHCP 池/静态租约、DNS | `tests/managed-router.py`：API 修改后检查客户端实际行为 |
| 防火墙 | 专属 nft 表、NAT、DNAT 增删、转发 MAC 阻断；LAN-only Web/宿主 SSH 端口保护覆盖 IPv4/IPv6 | 同一有线 VM；IPv6 管理面隔离不表示支持 IPv6 路由；MAC 转发阻断不表示同桥二层隔离 |
| 应用与恢复 | 不可变候选、串行作业、last-good、失败恢复、重启恢复、坏草稿诊断 | 有线 VM 的失败注入/重启/改址；不增加 LAN 改址倒计时或超时自动回滚 |
| 无线 | 显式单 AP、WPA2、独立 hostapd、检测及受支持信道校验 | `tests/managed-wifi.py` 使用 mac80211_hwsim 真实认证、租约、关闭/重开；不证明实体芯片/射频性能 |
| Web/API | 同源登录、保存/重读、异步应用反馈、状态来源与能力限制、桌面/移动布局 | 两个 `tests/web-*.cjs`；真实临时 API 与注入响应的边界见测试说明；UI 模拟不作为网络证据 |
| 系统/SSH | managed 下 SSH 和系统身份由宿主 NixOS 管理；有配置备份/恢复及账户操作 | Web/API 测试；不能宣称已实现完整设计中的所有系统/SSH 运行修改 |

源码入口：[managed 引擎](../backend/internal/managed/manager.go)、[配置校验和渲染](../backend/internal/managed/render.go)、[managed 模块](../modules/managed.nix)、[基础模块](../modules/default.nix)。

## 尚未完成的通用工作

这些项目继续追踪，不因页面禁用或后端拒绝而视为完成。

| 项目 | 当前差距 | 完成所需证据 |
|---|---|---|
| API 与特权执行隔离 | managed 设置 `privilegedApply=true`，基础模块因此以 root 运行 API；尚未满足完整设计第 5 节 | 低权限 API、独立受限执行器；对端认证、接口/路径/动作授权的负向测试；迁移后重跑真实网络及恢复测试 |
| PPPoE | managed 校验明确拒绝 | 模拟接入服务器上的拨号、断线重拨、凭据失败、真实出口 NAT |
| IPv6 路由 | managed 拒绝启用 IPv6/RA/PD | 上游获取/PD、LAN 地址与路由、双栈流量和管理隔离 |
| 多 AP/访客 | managed 拒绝访客/不支持组合 | 能力裁剪与真实无线客户端；访客二层及三层隔离、禁用清理 |
| QoS/家长控制/DDNS/UPnP | managed 拒绝启用 | 吞吐/时间规则/协议更新/映射创建撤销，含失败路径和停用清理 |
| 可选插件 | managed 拒绝启用；旧生成器或服务钩子不能证明完整生命周期 | 按完整设计第 7 节逐个验证启用、配置、生效、失败、禁用及 overlay 信任边界 |
| 其余完整设计契约 | A01–A14 尚无全部完成证据；配置版本/并发、系统操作等须逐项审计 | 对应源码、真实 API/浏览器、部署和网络证据，不能仅靠基础候选测试推断 |

实体 gw 测试仍须用户验收后决定；尚未执行。服务权限拆分属于通用框架实现工作，与提供 gw 专用配置无关。

## 本次核实的 CI 与历史记录

PR #16 的 `2b301da`：Go Tests [42586226383](https://github.com/dnfn-tech/nixos-router/actions/runs/42586226383) 和 Browser Tests [42586226369](https://github.com/dnfn-tech/nixos-router/actions/runs/42586226369) 已通过。NixOS VM Tests [42586226292](https://github.com/dnfn-tech/nixos-router/actions/runs/42586226292) 在本次记录时已通过模块契约步骤，三个 VM check 的构建步骤仍运行；不得将该记录解读为 VM 已通过。后续状态以该 run 与对应 SHA 为准。

- [M1–M15 历史实现记录](history/implementation-status-m1-m15.md)：保留阶段设计与旧验证描述，不再作为当前能力入口。
- [2026-10-28 历史候选报告](gw-readiness.md)：记录 `854fe60` 的本地构建、VM 和预览结果，不代表当前分支制品。
- [可复现测试命令](testing.md)：Go、模块、浏览器和 VM 验证。
