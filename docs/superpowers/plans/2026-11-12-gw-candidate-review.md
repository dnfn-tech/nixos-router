# gw 候选整体代码审查

审查对象：`feat/20261109-managed-ipv6`，基础提交 `4d0e9c1fa62a7051d97e7b9290fe8a1945315f1f` 加本轮工作树（包含未跟踪实现文件）。按 requesting-code-review 流程完成一次独立只读整体审查；审查者未修改文件、未委派其他代理，也未把父任务的测试记录算作独立实测。

结论：未发现可证实的 Critical/Important 代码缺陷。检查覆盖写前资源 union、generation/跨进程锁、IPv6 地址/路由/MTU/过期清理、DNS 来源合并、WiFi 归属/切换/失败恢复、秘密权限、实际无线能力预检、Nix 服务与管理器互斥，以及两份前端一致性。此结论不是整体交付通过。

审查指出的 Important 验收缺口：公开接受的 PPPoE6 需要真实 PPPoE/IPv6CP、DHCPv6/PD、MTU/MSS、断线重连、重启与关闭组合测试，IPv4 PPP 或 Ethernet IPv6 测试不能代替。实施与结果跟踪于 [PPPoE6 验收](2026-11-12-pppoe6-acceptance.md)，未取得终态前保持未完成。

审查时仍待执行的交付门槛：无线组合终态（随后 WiFi6/7 与双IPv6+WiFi7 均已通过，详见无线计划）、固定候选提交的适用复验、nixos-home 固定全部输入后的 gw 评估与系统构建。只有这些证据齐备，才能交付硬件验收候选。

明确不纳入本次软件交付：已排除的 LAN 倒计时回滚；实体 MT7927/CN射频/吞吐/稳定性；设备接口/驱动/秘密的具体值；DFS/6GHz/MLO/多AP/QoS/DDNS/家长控制等长期项。普通失败恢复仍必需；overlay 不自动获得 LAN 或管理信任。


PPPoE6 增量复核（2026-11-12）：同一独立审查者只读检查后续 net-init 修复、ISP fixture、完整协议断言、能力声明及 CI 接线，未发现有证据的 Critical/Important 代码问题。同步 net-init 只作用于专用逻辑接口且保留 generation 检查，未扩大外部资源接管范围；ISP 兼容处理未伪造 CPE 地址、前缀或回调。审查者核对完整日志八项/286.13s 与制品，前述 PPPoE6 Important 验收缺口关闭。

证据边界：DNS 验证覆盖两种来源合并及实际解析，没有单独隔离 IPv4 DNS 来证明每次查询走 IPv6 上游；不构成本次阻断。PPPoE6 与独立无线的所有排列未逐一组合测试；分别保留 PPPoE6 协议与双网 IPv6 的证据边界。增量源码审查通过不等于交付门槛完成，仍须 IPv4 PPP 回归终态、固定候选提交及适用检查、nixos-home 最终构建。
