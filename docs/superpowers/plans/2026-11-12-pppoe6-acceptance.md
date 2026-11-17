# PPPoE6 真实协议验收

整体只读审查发现公开配置与执行路径已有 PPPoE6，但现有真实 PPP VM 仅证明 IPv4；Ethernet DHCPv6 不能替代 IPv6CP/重连路径。保持功能范围，补组合证据。

使用隔离三节点 CPE/有线客户端/ISP，真实 rp-pppoe/pppd PAP+IPv6CP；ISP IPv6-up hook 仅配置 ISP 侧链路并启动真实 Kea DHCPv6-PD 和 radvd，不注入 CPE 事件。CPE 配置、地址、路由、DNS、通告与清理全部来自公开 Apply 和协议。ISP MRU1400 使 CPE 实际协商 MTU1400；在 ISP 后增加 MTU1500 的互联网命名空间，双向大 TCP 与两端初始 MSS 验证避免 PPP peer 本地终止掩盖缺失钳制。

验收：真实双栈流量及两来源 DNS、无 NAT66、WAN 管理拒绝、DHCPv6自然过期恢复、PPP 对端关闭/重连、CPE pppd 崩溃/恢复、整机重启、关闭IPv6保持IPv4、切回Ethernet DHCP后资源清理。固定候选后复验；不操作实体设备。

依据：[pppd 上游手册](https://ppp.samba.org/pppd.html) 的 IPv6 选项和 IPv6-up/down hook；实现前已核查仓库已有 private generation、独立 wan6 和逻辑 PPP 出口接线。此计划不新增生产功能，目的是补齐已有公开路径证据。

实施：新增tests/vm-managed-pppoe6.nix与managed-pppoe6.py，加入check。仅ISP hook配置ppp0地址/聚合回程，真实Kea/radvd随IPv6CP启动；CPE全由Apply及协议驱动。MSS端点在ISP后MTU1500命名空间，检查两端初始MSS与双向2MiB负载/无NAT66。构建会话14462、日志 `/tmp/router-pppoe6-vm.log`、制品 `/tmp/router-pppoe6-vm`，结果待确认。


测试环境排障：首次运行暴露 Kea 的运行目录要求，第二次明确租约数据库仅允许 `/var/lib/kea`；已统一 RuntimeDirectory/StateDirectory 和租约路径。第三次运行（`/tmp/router-pppoe6-vm-state.log`）确认 Kea 将 PPP peer 的链路本地地址误作本地地址绑定，UDP547 因 EADDRNOTAVAIL 未启动；其 Linux `ipaddrs_get` 读取 IFA_ADDRESS，与日志的 CPE 地址完全吻合。仅 ISP IPv6-up hook 将协商得到的本地链路地址改为不带 peer 属性的 /64，保留到对端的链路路由；不修改 CPE、不注入租约或回调。独立用户/网络命名空间实验确认单独 replace 不会移除 peer；显式删除/重建后本地 UDP547 可绑定、对端仍由链路路由可达。上游参考：https://github.com/isc-projects/kea/blob/master/src/lib/dhcp/iface_mgr_linux.cc 。新一轮日志 `/tmp/router-pppoe6-vm-local.log`，结果待确认。


完整结果：`/tmp/router-pppoe6-vm-ready.log` 286.13s，八项子测试全部通过，构建退出0，制品 `/tmp/router-pppoe6-vm`。产品修复是将 PPP 逻辑接口的内核 RA/自动配置开关提前至同步 net-init，避免 IPv6CP 完成至 wan6 子服务启动之间收到首个 RA 后形成非自有资源；原拒绝覆盖非自有资源的保护保留。DHCPv6 恢复检查明确等待实际 IA_NA，而非用服务 active 替代协议完成。能力声明定向测试先 RED 后 GREEN，现包含 pppoe6。共享钩子的 IPv4 PPP 回归、最终固定提交复验和 nixos-home 构建仍需完成。
