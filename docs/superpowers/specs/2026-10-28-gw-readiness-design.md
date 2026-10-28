# gw 硬件验收前置版本设计

用户授权自行规划、实现和验证；本阶段不连接、不部署、不操作实体 gw。完成后由用户验收，再决定是否进入硬件测试。

## 目标与门槛

交付首轮 gw IPv4 基本网关硬件验收候选：WAN DHCP/静态、单 LAN 网桥、dnsmasq DHCP/DNS、NAT、端口转发、MAC 阻断、LAN-only 管理、单 AP WPA2、失败恢复和开机恢复。当前本地 gw 源配置使用 enp3s0 / br0 / enp4s0-enp6s0 / wlp1s0；这是配置快照，不是硬件事实。本阶段用虚拟网口复现，不直接使用这些名字操作开发宿主。

完整家用路由目标保留。PPPoE/IPv6/QoS/DDNS/家长控制/多AP访客/第三方插件若尚未具备真实执行能力，必须在 managed 模式明确报告不支持并拒绝启用，不能作为已完成项。第一轮硬件候选的范围是明确的 IPv4 单 LAN 基本网关，不把该门槛冒充所有扩展功能已完成。SSH及系统账户由NixOS配置提供；未受支持的SSH运行修改不得静默成功。

硬件门槛：Go普通与并发测试、浏览器回归、Nix模块评估/构建、真实隔离网络的API配置与行为变化、无线模拟软件链路、故障与重启恢复均需实测通过；所有阻塞审查问题修复。缺失证据时报告未就绪，不填写通过。

## 方案选择

保留Docker预览；新增显式managed-router模式。继续依赖通用networkd会违背已确认的独立管理器边界；直接在gw开发会越过用户保留的硬件批准门槛。采用独立Go运行管理器加NixOS专属协议服务，并在VM运行完整应用链路。

## 运行与持久化

- 新包 internal/managed 负责受管接口校验、候选生成、原生命令预检查、激活、验证及失败恢复。仅操作显式允许的接口和专属nft表，命令使用argv和context超时。
- WAN DHCP由专属udhcpc协议客户端提供，不启动另一通用网络管理器。静态地址与路由通过ip命令设置；LAN桥及端口由管理器建立。
- dnsmasq、hostapd分别读取状态目录中的完整专属配置；密码文件0600，不写入Nix store或日志。DNS上游可显式配置；DHCP租约路径固定给状态API使用。
- nft在自身表内原子替换，WAN拒绝管理入站，forward默认拒绝未授权入站，NAT出口限定WAN，MAC阻断作用于转发。不能清空整个ruleset。
- Apply串行且重复执行无资源累积；记录不可变候选和最终结果。最近成功配置与用户已保存草稿分离，重启恢复最近成功配置。应用步骤失败恢复上一成功运行配置；不新增LAN改址超时回滚。
- 首次启动仅使用显式引导配置建立受管网络。已有last-good时不能把未应用草稿在重启时自动激活。
- API并发保护和精确Origin校验，命令超时；运行模式与受支持功能必须可查询。

## 隔离验收

拓扑：上游ISP -- WAN/router -- LAN/client；附加LAN客户端用于区别MAC阻断，软件无线使用mac80211_hwsim或等效隔离VM链路。router不预设LAN桥、地址、NAT、dnsmasq，必须由routerd从引导/HTTP配置创建；上游和客户端可使用系统网络管理器。

测试：DHCP/DNS/NAT实测、改变地址池及DNS、转发添加删除、MAC阻断与其他客户端不受影响、WAN管理拒绝、NAT禁用生效、重复Apply、错误候选无破坏、应用阶段失败恢复、进程/整机重启、草稿不被自动应用、状态来源真实。无线先验证hostapd配置和模拟关联/WPA/DHCP，MT7927能力与射频性能明确留给后续硬件验收。

无/dev/kvm时优先尝试QEMU TCG，不能以mock测试替代真实网络证据。只有隔离虚拟机/容器内可修改网络，不修改开发宿主网络。

## 交付

可重跑命令、日志/版本/制品摘要、逐项证据矩阵、gw迁移草案（Kea→dnsmasq、独立无线网段→单LAN）、人工控制台恢复步骤。未获后续批准前不在gw执行任何命令。代码保留当前独立worktree，原主工作区暂存不动。

## 集成审查补充契约

- `managed.interfaces` 只含物理有线 WAN/LAN 成员；桥由引擎创建，WiFi 通过独立 `wifiInterface` 授权，不能混入 wired allowlist。
- last-good 使用原子替换并同步目录；持久化失败必须恢复上一配置。损坏 last-good 拒绝启动；损坏草稿保留原文件并从有效 last-good 恢复，健康接口显示启动警告。
- 首版拒绝已有 last-good 后修改桥名/WAN 物理接口，避免资源迁移中断造成不可证明的恢复。普通 LAN 地址、池、DNS、策略修改仍支持，不新增 LAN 改址自动回滚。
- DHCP 回调使用 generation fencing；WAN 无租约时结果明确为 awaiting-wan-lease，不能解释为互联网可达。
- API 在激活网络前发送并刷新 202 作业编号；状态读取使用已应用快照，不能拿保存草稿冒充运行态。
- dnsmasq 专属单元用 forking 启动握手，绑定失败应阻止提交；保留 root 读取私有目录内原子更新的 WAN DNS 文件。配置、PSK、状态目录仍为0600/0700，不向通用服务共享。
- hostapd 必须通过控制接口报告 AP-ENABLED 对应的 `state=ENABLED`，不能仅凭 systemctl is-active 持久化成功。
- nft 仅管理自己的 inet 表；管理面限制同时覆盖IPv4/IPv6。MAC阻断指经过路由器转发的流量，不宣称同网桥客户端二层隔离。
- 真实 VM 必须让 LAN 客户端发起 HTTP 配置应用；测试驱动用独立控制通道，不能预设被测网桥、地址、NAT。软件无线必须关联、认证和获得租约，配置语法检查不算通过。

## 真实网桥归属问题的复现与修复

首轮TCG通过DHCP/DNS/NAT后，重复Apply拒绝自有桥。独立user+net namespace复现：`ip link add name br-test alias nixos-router-managed type bridge`返回成功，但`ip -j -d link show dev br-test`没有ifalias。将alias改为后续独立`ip link set dev br-test alias nixos-router-managed`后字段才出现。引擎已按此修正并保留外部桥拒绝逻辑；打标失败只清理本事务刚创建的桥。模拟runner回归覆盖重新应用和打标失败，真实VM仍须重新通过。


## 最终审查修复

恢复时不仅信任last-good列出的旧成员，还核对自有桥的真实成员，移除不在目标LAN集合中的已授权有线成员；WiFi使用独立授权清理。不得触碰未授权接口。真实VM在新增eth3已入桥、协议服务启动暂停的窗口强杀后台，验证恢复移出eth3且last-good摘要不变。

SSH隔离端口经NixOS services.openssh.ports→环境变量→CLI严格解析→managed.Options.SSHPorts传入。HTTP草稿不能选择被保护端口。真实VM使用sshd22/2223、草稿2222并关闭可选防火墙，仍确认LAN可达而WAN拒绝。

generation在事务结束（包括恢复）后回收，保留current及最近两份历史；预检失败候选立即删除。单测验证多次应用数量有界、当前目录保留和失败候选敏感文件清理。最终统一VM与源码清单见gw-readiness报告。
