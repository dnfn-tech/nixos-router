# gw 有线与无线双网段

本项落实已授权集成目标 G02–G04、G07–G10/G12。现有无线桥接模式保持兼容；独立网段不能通过默默桥接到现有 LAN 来替代。实现前先明确以下通用契约；设备地址由集成方给出。

## 方案与访问策略

采用 `wifi.bridgeToLan=false` + `wifi.network` 显式配置无线 IPv4 CIDR、DHCP 池和静态租约，在 allowlist 中唯一 WiFi 接口上配置网络，不额外创建无用途的桥。`bridgeToLan=true` 保持现有行为。无须把所有 LAN 结构重写为无限网络数组，当前仍限制单 AP；这种通用重构会扩大状态与 UI 迁移风险而非交付 gw 必需能力。

独立无线网段具有显式管理与有线互访设置（默认拒绝、由集成方明确开启）；有线原管理访问保持不变。两侧到实际 WAN 均可出站并对 IPv4 NAT；允许对应 established/related 返回。overlay 不因这一功能获得 LAN 身份。接口集合在 IPv4/IPv6 输入、转发与 DNS 中一致，原 TCP 管理端口保护不能被可选防火墙开关绕过。

IPv6 从同一 IA_PD 为每个内网稳定分配不同 /64：有线使用第0个，无线第1个。/64 委派不足以承载两个网段时明确报错/清理失效委派，不能重复使用同一前缀；WAN RA/SLAAC 和 IPv4 状态不受 PD 容量不足牵连。前缀资源、路由与通告仍以 generation 私有日志追踪，不扩大清理范围。

## 顺序及验证

1. 配置字段与 typed field errors：保留旧文档兼容；启用独立 WiFi 时验证 IPv4、主机地址、掩码、DHCP 范围/顺序/租期、静态租约及 LAN/WAN 子网不重叠；在任何命令前失败。禁用 WiFi 的草稿保持可编辑。
2. 生命周期：观察外部 bridge/master/地址归属，拒绝接管；停止 hostapd 后重配已授权接口，记录并精确撤销本管理器安装的地址；桥接↔独立/禁用、重启、失败恢复均一致。不得借助清空外部接口绕过归属检查。
3. dnsmasq 多接口、按接口标记 DHCP/DNS/网关；双网段租约与明确上游/域名。hostapd 独立模式不带 bridge 行。
4. nft 输入/转发/IPv4 NAT 的网络集合与显式互访策略。隔离命名空间报文测试覆盖有线、无线、WAN三侧的正反例、真实 NAT 和外部表保留。
5. generation IPv6 context/PD 资源扩展为两个内网；真实协议 VM 证明不同 /64、无 NAT66、失联撤销、更新、禁用与重启恢复，PD不足用例明确失败。
6. hwsim 站点与有线客户端各自获取正确地址/网关/DNS并联网；矩阵管理和互访正反例；桥接切换与失败恢复。Web/API 编辑与状态展示配套，双份资源同步、浏览器回归。

以上是实施计划，未完成项不得在 capability 或集成表中标为已支持。WPA3/HE/EHT 与双网段分别验收，最终候选固定提交后再复验组合。

当前进度：新增 wifi.network 草稿模型（IPv4CIDR/DHCP/StaticLeases/AllowManagement/AllowWiredAccess），仅在启用独立 WiFi 时校验；桥接/禁用保留草稿。缺字段 RED `/tmp/router-routed-wifi-config-red.log`，GREEN `/tmp/router-routed-wifi-config-green.log`；追加静态租约重复/MAC单播/主机名注入边界，主机名 RED `/tmp/router-routed-wifi-hostname-red.log` 后实现严格标签校验；全量 Go91213终态退出0 `/tmp/router-routed-wifi-foundation-go.log`。运行 apply 仍由原门禁拒绝独立 WiFi，不能宣称已支持双网段。

## 2026-11-12 实施与证据更新

- IPv4 归属日志写入前记录待变更地址并保留失败两侧的 union，重启重试可恢复；只对授权无线接口执行精确 `addr add/del`，不清空地址或路由。root 私有文件拒绝 foreign interface、重复/非法地址、额外字段与链接。测试 RED `/tmp/router-wifi-ipv4-red.log` 后 GREEN；真实内核首次发现 `ip -4 addr show` 省略无 IPv4 接口，修正为全部地址观察后筛选 IPv4，`/tmp/router-wifi-ipv4-kernel2.log` 通过，保留外部地址和路由。
- 双网段 dnsmasq 采用 wired/wifi 范围 tag，下发各自网关与 DNS；独立 hostapd 不写 bridge。nft 扩展 IPv4 NAT、明确的输入服务和双向互访开关；撤销策略位于 established 接受之前。可选 WAN filter 关闭不移除独立无线区的边界。
- `TestRoutedWiFiApplyTransitionsAndFailureRecovery` 验证有 last-good 的失败恢复，以及独立→桥接→独立→禁用。RED `/tmp/router-wifi-apply-red.log`；GREEN `/tmp/router-wifi-apply-green2.log`。全量 Go27422终态退出0 `/tmp/router-wifi-routed-go.log`。
- 首次完整 routed WPA3/hwsim VM25020终态退出0，218.02s，`/tmp/router-wifi-routed-vm.log`，制品 `/tmp/router-wifi-routed-vm`。真实两侧租约/网关/DNS、远端观测 NAT 源地址、管理/互访授权及撤销、禁用和执行器/整机恢复通过。
- 扩展 VM67905终态退出0，315.89s，`/tmp/router-wifi-routed-extended-vm.log`，制品 `/tmp/router-wifi-routed-extended-vm`。额外覆盖 optional WAN filter 关闭仍保持隔离、真实 hostapd ExecStartPre 单次失败后恢复旧地址及流量、routed↔bridge 切换；前述回归保持通过。此 VM 快照早于后续私有双网段 IPv6 接线，最终候选仍须组合复验。
- Web 新增独立无线网段、DHCP、静态租约与访问设置；切回桥接保留草稿，激活后非法 CIDR 定位字段。RED `/tmp/router-wifi-routed-web-red.log`，GREEN `/tmp/router-wifi-routed-web-green.log`，46项完整浏览器回归73402终态退出0 `/tmp/router-wifi-routed-web-full.log`；双份资源完全一致。
- IPv6 新增 bounded dual /64 allocator（跳过 excluded 范围，容量不足无部分分配）、授权 WiFiLAN runtime context、精确双接口资源更新/撤销、两侧动态 RA constructor 和 scoped forwarding/cleanup。/64容量不足时先撤销两个 LAN 的旧资源并保留独立 WAN，再返回明确容量错误。定向 RED/GREEN 日志 `/tmp/router-dual-{prefix,resources,services}-{red,green}.log`。
- 双 IPv6 内核测试最初使用单元夹具固定时钟，真实时间被判未来；测试切为真实时钟后59086终态退出0，`/tmp/router-dual-resources-kernel2.log` 1.315s（同时含 WiFi IPv4），验证两个不同 /64 的真实地址与路由、精确清理和外部资源保留。全量 Go43761退出0 `/tmp/router-dual-foundation-go.log`。
- **当前验证中**：公开 Apply/Restore 的 routed WiFi + IPv6 限制已在定向 RED/GREEN 测试后移除；新增真实 Kea、hwsim/WPA3 与有线客户端的双网协议 VM。首次构建因 Python 网络类型联合类型被检查器拒绝，改为显式 IPv6Network 后重建；会话22130、日志 `/tmp/router-dual-protocol-vm.log`。已进入实际协议测试，完整前缀变更/容量不足/过期/重启结果仍待终态，不能宣称完整验证通过。
- 公开路径最新全量 Go72187终态退出0，`/tmp/router-dual-public-full-go.log`，managed15.511s/server37.040s。WiFi6/7/80MHz、能力声明、最终审查/固定提交/PR/nixos-home 构建继续保留。

## 双网 IPv6 完整协议验证结果

会话22130终态退出0，`/tmp/router-dual-protocol-vm.log`，390.98s，制品 `/tmp/router-dual-protocol-vm` 指向 `/nix/store/qzw1sv5rxspj66wk1nm5bnwk0k4m0257-vm-test-run-vm-managed-ipv6-dual`。全部七项通过：不同 /64/WPA3/DNS/无NAT66、管理与互访授权撤销、真实ISP前缀变更、/64容量不足撤销及恢复、DHCPv6自然过期及恢复、整机重启55.15s、关闭IPv6双网清理17.60s。此结果覆盖当前软件路径，尚未固定提交，不证明实体射频或完整gw集成。
