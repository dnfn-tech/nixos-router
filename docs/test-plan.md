# nixos-router 测试计划（v1 · docs-first）

本文面向 nixos-router 的 v1 能力，覆盖核心 WebUI/后端与可选插件模块，确保“单一意图配置（config.json）+ 运行态 SQLite”方案在家用路由场景下稳定可用、安全默认、可回滚。

## 1. 目的 / 范围 / 不在范围
- 目的：验证 WebUI + API + Apply 管道在目标环境上按设计工作；验证 LAN-only 管理、安全默认、失败回滚；验证插件启停对导航/页面与运行态的影响。
- 范围：
  - 核心：登录/会话（LAN-only）、概览与网口、WAN（DHCP/静态/PPPoE）、单一 `br-lan`、dnsmasq（DHCP 池 / 静态绑定 / 内网域名 / 上游）、WiFi 能力裁剪（AP≤1 与 ≥2）、访客 BSS（同 LAN、隔离）、设备列表、nftables 防火墙 + 端口转发、UPnP、SSH、系统（备份/恢复/审计/重启）、保存/应用状态机、`config.json` SoT + SQLite。
  - 插件（可选）：QoS、家长控制、DDNS、IPv6、AdBlock、Traffic、Mihomo、VLAN（关闭时默认单一 LAN）、Tailscale（official/selfhost/headscale 三态）、Zerotier（official/selfhost）。
- 不在范围：
  - 非 v1 的 Mesh、全面 VPN 门户、长周期流量大盘、广告过滤高阶策略、第三方外置插件协议细节实现等。

## 2. 测试环境
- Dev VM：NixOS 虚机（QEMU/KVM），仿真多网口；可注入 nl80211 能力的假驱动或“无线缺失”情景。
- 目标主机 `gw`：N5105 + 1×WAN + 3×LAN，WiFi 模块（示例：MT7927，AP≤1）；参考 `docs/target-host-notes.md`。
- Mock UI：当前 `uploads/index.html` 交互原型用于可视化对齐（只读演示），真实测试以后端 API + 前端构建为准。

## 3. 入口/退出准则
- 入口：基础环境可启动（journald 可见 api/worker 单元均 healthy），`/var/lib/nixos-router/` 可写；具备上/下行网络与最小 dnsmasq/hostapd/nftables 依赖。
- 退出：所有 P0/P1 通过，P2 不影响发布；关键回滚场景通过；审计/日志可定位失败因；插件启停矩阵通过。

## 4. 测试类型
- 单元：Schema 校验、字段语义校验（IP/端口/冲突/范围）、生成器（nftables/dnsmasq/hostapd/ppp 等）Golden Tests。
- 集成 / API：REST 端点契约、权限/CSRF、作业状态机、能力探测接口。
- Apply 管道：validate → 原子写 → 生成配置 → reload/restart → 审计；失败/回滚。
- E2E WebUI：登录 → 编辑 → 保存并应用 → 结果可见；导航/页面随插件启停变化。
- 安全：LAN-only 监听、CSRF、Cookie 属性、默认防火墙策略；敏感字段脱敏导出。
- 回归：对既有成功用例的周期性重跑。
- Soak/稳定性：长时间运行、断连/抖动、反复 apply、PPPoE 断线重拨。

## 5. 需求可追溯矩阵（节选）
| 功能/需求 | 文档参考 | 测试用例 |
|---|---|---|
| LAN-only 管理 | requirements: 安全；architecture: API 监听 | CORE-SEC-001/002 |
| 保存/应用状态机 | requirements: UX 状态机；architecture: 作业 | CORE-APPLY-001..006 |
| WAN DHCP/静态/PPPoE | requirements: WAN；pppoe-notes | CORE-WAN-001..006 |
| 单一 `br-lan` + dnsmasq | requirements: LAN + DHCP；architecture | CORE-LAN-001..008 |
| WiFi 能力裁剪 | requirements: WiFi；prototype | CORE-WIFI-001..006 |
| 访客 BSS 隔离 | requirements: WiFi | CORE-WIFI-007..009 |
| 防火墙/端口转发 | requirements: 防火墙/NAT | CORE-FW-001..006 |
| UPnP | requirements: UPnP | CORE-UPNP-001..003 |
| SSH | requirements: SSH | CORE-SSH-001..003 |
| 系统备份/审计/重启 | requirements: 系统 | CORE-SYS-001..005 |
| 插件启停 → UI 可见性 | plugins: UI 控制；architecture: UI REST | PLG-UI-001..004 |
| QoS/家长/DDNS/IPv6 | requirements + plugins | PLG-QOS-001..；PLG-PRN-001..；PLG-DDNS-001..；PLG-IPV6-001.. |
| AdBlock | plugins/adblock | PLG-ADB-001..004 |
| Traffic | plugins/traffic | PLG-TRF-001..003 |
| Mihomo | plugins/mihomo | PLG-MHM-001..005 |
| VLAN | plugins/vlan | PLG-VLAN-001..006 |
| Tailscale（三态） | plugins/tailscale | PLG-TS-001..006 |
| Zerotier（两态） | plugins/zerotier | PLG-ZT-001..004 |

## 6. 详细测试用例（节选 · 可扩展）

### CORE-SEC-001 LAN-only 监听
- 前置：设备接入 LAN；无 WAN 入站。
- 步骤：从 LAN 访问 WebUI；从公网/WAN 访问尝试。
- 期望：LAN 可访问；WAN 连接被拒；nftables 未放行 WAN 侧管理端口；审计记录 LAN 登录。
- 优先级：P0

### CORE-SEC-002 CSRF/Cookie 属性
- 前置：已登录会话。
- 步骤：检查 Set-Cookie 属性（HttpOnly、SameSite）；跨站 POST 尝试。
- 期望：Cookie 安全属性正确；跨站请求被拒绝；审计记录异常尝试。
- 优先级：P0

### CORE-APPLY-001 保存并应用（成功路径）
- 前置：合法配置修改（DNS 上游）。
- 步骤：PUT /api/v1/config → POST /api/v1/apply；轮询作业。
- 期望：validate 通过、原子写成功、生成/重载成功、作业成功、审计写入；UI 状态从 “未保存/应用中” → “已保存”。
- 优先级：P0

### CORE-APPLY-005 失败与回滚
- 前置：构造非法端口转发（端口冲突）。
- 步骤：保存并应用。
- 期望：作业失败；运行中配置未被破坏；可一键回退至 last-good；审计包含失败原因。
- 优先级：P0

### CORE-WAN-001 DHCP
- 步骤：设置 WAN=DHCP，应用。
- 期望：获取地址/网关/DNS；概览展示已连接；nftables 放行 LAN→WAN。
- P0

### CORE-WAN-004 PPPoE 正确/错误凭据
- 步骤：设置 PPPoE 正确账号/密码；后改为错误密码。
- 期望：正确时 `ppp0` 拨号成功并 NAT；错误时重试/失败上报且不影响现有配置；日志/审计可见。
- P0

### CORE-LAN-001 `br-lan` + dnsmasq 池
- 步骤：设置网关、池起止、租约；应用；接入客户端。
- 期望：客户端获得地址；租约列表可见；静态绑定生效且地址不再被池分配。
- P0

### CORE-WIFI-001 无无线硬件
- 步骤：能力探测返回 unsupported。
- 期望：WiFi 页禁用并提示；API 返回 unsupported。
- P1

### CORE-WIFI-003 能力裁剪 AP≤1
- 前置：设备报告 `AP≤1`。
- 步骤：同时启用 2.4G+5G 尝试。
- 期望：被前端/后端拒绝；仅单 AP 允许；访客以 BSS 形式（若支持），否则禁用。
- P0

### CORE-WIFI-007 访客 BSS 隔离
- 步骤：启用访客 SSID；从访客客户端尝试访问 `br-lan`。
- 期望：互联网可达；`br-lan` 不可达；nftables 规则生效。
- P0

### CORE-FW-002 端口转发
- 步骤：配置 TCP 8443 → 192.168.88.20:443；应用；从 WAN 测试。
- 期望：转发生效；nftables 自动放行 WAN 外部端口；审计记录。
- P0

### CORE-UPNP-001 开启/关闭 UPnP
- 步骤：开启 UPnP；查询映射；关闭。
- 期望：开启后运行 miniupnpd；列表可见；关闭后无活动映射。
- P1

### CORE-SSH-001 SSH 策略
- 步骤：启用 SSH，禁用密码/root；尝试密码登录与 root 登录。
- 期望：策略生效；LAN 防火墙放行 22；不允许的尝试被拒。
- P0

### CORE-SYS-002 备份/恢复
- 步骤：导出 `config.json` 脱敏/完整包；上传恢复。
- 期望：脱敏导出遮蔽敏感字段；完整包含 DB；恢复应用且审计记录。
- P0

### PLG-UI-001 启用/禁用 → 导航可见性
- 步骤：通过 `PATCH /api/v1/plugins/<id>` 切换状态；查询 `GET /api/v1/ui/nav`。
- 期望：启用出现入口与页面；禁用隐藏；禁用插件的 API 返回 404/disabled。
- P0

### PLG-ADB-001 AdBlock 生效
- 步骤：启用 AdBlock，添加列表；应用。
- 期望：生成 `dnsmasq.d` 片段；常见域名拦截为 0.0.0.0；reload 成功。
- P1

### PLG-TRF-001 Traffic 采集
- 步骤：启用 vnstat；产生流量；查询统计 API。
- 期望：时间序列可见；禁用后采集器停止。
- P2

### PLG-MHM-002 Mihomo fake-ip 模式
- 步骤：配置 `mode=fake-ip`；dnsmasq 上游指向 mihomo；为内网域名设置直出。
- 期望：fake-ip 生效；内网解析不受影响；文档中的绕过策略有效。
- P1

### PLG-VLAN-002 多 VLAN + 隔离
- 步骤：创建 VLAN10/20，分别设网段与 DHCP；开启隔离；应用。
- 期望：各 VLAN 客户端可获地址；跨 VLAN 访问被阻断；DNS 分段生效。
- P0

### PLG-TS-001 官方控制面
- 步骤：`controlPlane=official` 登录；advertise `192.168.88.0/24`。
- 期望：路由通告成功；nftables 放行；禁用撤销。
- P1

### PLG-TS-003 外部 selfhost Headscale
- 步骤：`controlPlane=selfhost` + `loginServer=https://headscale.example.com`；登录。
- 期望：tailscale up 使用 `--login-server`；接入成功。
- P1

### PLG-TS-004 本机 Headscale
- 步骤：`controlPlane=headscale`；设置 `listen/baseURL`；启用。
- 期望：`headscale.service` 运行；生成预授权密钥；本机 `tailscale up --login-server=$local`；仅 LAN 可访问 Headscale HTTP。
- P0

### PLG-ZT-002 Zerotier 自建控制面
- 步骤：`controlPlane=selfhost` + `controllerUrl` + `apiToken`；加入网络。
- 期望：成功加入；安装/通告路由；禁用撤销。
- P1

## 7. 插件矩阵（启停/可见性/Apply）
| 插件 | 启用时导航/页面 | 启用时 API | 启用时 Apply | 禁用时行为 |
|---|---|---|---|---|
| qos | 显示 | 可用 | 生成/应用 QoS 规则 | 隐藏 + 撤销规则 |
| parental | 显示 | 可用 | 生成家长规则 | 隐藏 + 撤销规则 |
| ddns | 显示 | 可用 | 生成 ddns 服务配置 | 隐藏 + 停止服务 |
| ipv6 | 显示 | 可用 | 生成 ip6 表/RA 等 | 隐藏 + 撤销 |
| adblock | 显示 | 可用 | 生成 dnsmasq 片段 | 隐藏 + 移除片段 |
| traffic | 显示 | 只读 | 启停采集器 | 隐藏 + 停止采集 |
| mihomo | 显示 | 可用 | 生成配置/启停服务 | 隐藏 + 停止 |
| vlan | 显示 | 可用 | 创建 VLAN/桥/DHCP/隔离 | 隐藏 + 回到单一 `br-lan` |
| tailscale | 显示 | 可用 | 启停 tailscaled/（headscale 可选） | 隐藏 + 停止/撤销 |
| zerotier | 显示 | 可用 | 启停 zerotier-one | 隐藏 + 停止/撤销 |

## 8. 负向/失败路径
- Schema 失败：字段缺失/范围错误 → validate 阶段失败，配置不落盘。
- 生成失败：nftables/dnsmasq/hostapd 生成异常 → 作业失败并保留旧配置。
- 能力拒绝：WiFi AP 组合超出硬件能力 → 校验拒绝并提示。
- PPPoE 凭据错误 / 认证失败 → 拨号失败状态与重试，现网不受影响。
- 端口冲突：端口转发/UPnP 与服务冲突 → 拒绝或优先级规则，日志提示。
- 外部依赖不可达：AdBlock 列表下载失败 → 使用上次缓存并告警；DDNS Token 错误 → 状态失败。
- tailscale/zerotier 控制面不可达 → 客户端不登录并回退；headscale 启动失败 → 禁止切 `headscale` 模式通过。

## 9. v1 里程碑验收清单
- [ ] LAN-only 管理、CSRF/Cookie 安全属性
- [ ] 保存/应用成功路径 + 失败回滚
- [ ] WAN（DHCP/静态/PPPoE）与 NAT 正常
- [ ] 单一 `br-lan` + dnsmasq（租约/静态）可用
- [ ] WiFi：无硬件禁用页；AP≤1 裁剪；访客 BSS 隔离
- [ ] 防火墙 + 端口转发 + UPnP（可开关）
- [ ] SSH 策略（密码/root）受控
- [ ] 系统：备份/恢复/审计/重启
- [ ] 插件 UI 控制（启停隐藏入口/404）；至少 QoS/DDNS/IPv6/AdBlock/Traffic 验证通过
- [ ] Tailscale（official/selfhost/headscale）与 Zerotier（official/selfhost）路径验证

## 10. 风险 / 延后项
- 不同无线芯片 nl80211 能力差异导致的配置组合复杂性 → 加强能力探测与后端校验。
- PPPoE/IPv6 在不同运营商侧差异 → 增加可配置超时/重试策略。
- AdBlock 列表体积/更新失败 → 引入缓存/增量更新与超时控制。
- 远程接入（tailscale/zerotier/headscale）安全边界 → 明确仅 LAN 放行、默认不对 WAN 暴露。
- 热加载（v1 采用冷加载） → 后续评估。

