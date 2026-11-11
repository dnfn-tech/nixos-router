# gw 无线模式与带宽实施计划

落实已批准的 G06；设备驱动、监管域实际值、校准和秘密由 nixos-home 提供。保持单 AP 和现有空字段文档兼容，不借此增加多 AP、MLO、6GHz 或 DFS。

## 通用配置契约

AP 增加 `standard`（空值为既有 legacy 配置；wifi4/wifi5/wifi6/wifi7）和 `channelWidthMHz`（空值按20；显式20/40/80）。20MHz 不要求扩宽；40/80MHz 必须有显式标准，80MHz 只允许5GHz及至少wifi5。wifi5 只允许5GHz；wifi4 的上限40MHz；wifi6/7 可以在2.4/5GHz启用。当前允许的非DFS5GHz主信道继续保留，165仅20MHz；40/80MHz 的所有子信道必须均受驱动/监管域允许。2.4GHz40MHz副信道方向由有效主信道确定，不能向监管域外扩宽。

字段类型、未知枚举、带宽及组合错误给出对应字段路径。任何运行时能力不足必须在修改网络前明确拒绝；不能静默降级到legacy/20MHz。中间实现阶段继续通过明确门禁拒绝尚未完成能力预检的高级模式。

## 实施顺序

1. 配置校验 RED/GREEN：旧文档往返、合法wifi6/7的5GHz149/80、错误枚举/宽度/频段组合，以及 inactive 草稿的既有约定。
2. 纯计算 radio plan：主/副信道、中心频率索引和全部20MHz子信道；表驱动边界测试。渲染HT/VHT/HE/EHT逐级能力，避免硬编码天线/MCS/beamforming能力。
3. 实际无线能力预检：读取授权接口对应PHY及 `iw phy ... info`，按频段和AP iftype核实HT/VHT/HE/EHT、带宽及全部子信道可发射状态。缺失信息明确失败；只读查询失败不能绕过。监管域/驱动的最终约束仍由hostapd执行。
4. 页面与能力API共享同一解释契约：标准和带宽可编辑，字段错误可定位；不把编译支持当作设备支持。两份前端保持一致。
5. 隔离hwsim实测hostapd运行、实际信道宽度与站点HE/EHT关联（仅模拟器确实提供的能力）；错误能力/禁用子信道明确拒绝。若模拟器不提供某能力，保留真实性，另用实际解析/配置检查覆盖软件路径并将实体能力验证列入硬件清单。
6. 全量Go、竞态/vet、浏览器、适用VM回归；最终固定提交后组合复验。

## 依据

固定 nixpkgs 的 hostapd 包版本2.12，已启用 IEEE80211N/AC/AX/BE。上游 `https://w1.fi/releases/hostapd-2.12.tar.gz` 已下载到 `/tmp/router-hostapd-2.12.tar.gz`，SHA256 与固定包的 `sha256-9DUCVhwoukerd+GOGpc9BzYcaMyLFBeOYZvVeWtw6r0=` 一致。该版本 hostapd.conf 中 VHT/HE/EHT 的80MHz宽度值为1，中心频率索引按对应频段规则计算；ap_config.c 明确 EHT 必须同时启用 HE。以此版本源码核实渲染，不沿用缺少EHT的旧文档。

当前仅有计划和上游证据，尚未实现该配置契约或宣称G06通过。

实施更新：配置新增standard/channelWidthMHz，未知值与激活组合给出字段错误，旧文档保持兼容；配置测试RED缺字段 `/tmp/router-radio-config-red.log` 后全包GREEN `/tmp/router-radio-config-green.log`。管理器门禁测试RED证实原路径会忽略新字段 `/tmp/router-radio-gate-red.log`，已添加明确拒绝直到实际能力预检/渲染接通；该门禁是中间状态，不是G06完成。

信道规划/渲染更新：新增 `config.PlanWiFiRadio`，计算20/40/80MHz中心信道、副信道方向与全部20MHz子信道；覆盖5GHz上下边界与2.4GHz正反副信道。测试RED `/tmp/router-radio-plan-red.log`，配置全包GREEN `/tmp/router-radio-plan-green.log`。hostapd逐级HT/VHT/HE/EHT配置、不指定硬件天线/MCS能力，80MHz149对应中心155；legacy渲染保持不变。测试RED `/tmp/router-radio-render-red.log` 后GREEN `/tmp/router-radio-render-green.log`。实际AP PHY能力预检和UI仍待实现，公开高级模式应用门禁继续保留。最新全量Go会话97501运行中，日志 `/tmp/router-radio-foundation-full-go.log`。

全量Go97501终态退出0：`/tmp/router-radio-foundation-full-go.log`，managed13.337s/server39.767s。

能力预检依据更新：固定iw版本6.17，下载 `/tmp/router-iw-6.17.tar.xz` 并验证SHA256与固定包 `sha256-fRguSYKJqzmyV9pngNVi5BU3cQf1A1juW1W4z+QLHjM=` 一致。上游info.c的频率输出仅打印disabled/noIR/radar等，没有NO_20MHZ/NO_HT40/NO_80MHZ；不能凭phy info判断全部宽度限制。必须追加只读 `iw phy <phy> channels`，按phy.c输出逐频率读取Channel widths: 20MHz/HT40-/HT40+/VHT80与No IR/Passive scan/Radar detection。HE/EHT依据util.c的Iftypes列表精确匹配AP；HE40/2.4GHz、HE40/HE80/5GHz须来自同一AP能力块，禁止跨频段或station能力合并。预检与API共用解析器，缺失数据明确拒绝；最终hostapd/驱动仍会验证。

共享解析器已实现于 `backend/internal/wifi`：按band保留HT/VHT与AP iftype限定HE/EHT；独立channels观察保留禁用/NoIR/radar/全部width。请求判定检查所有20MHz子信道、主信道HT40方向及80MHz限制，缺能力/限制子信道明确报错。解析测试RED `/tmp/router-radio-capabilities-red.log` 后GREEN `/tmp/router-radio-capabilities-green.log`；请求支持RED `/tmp/router-radio-support-red.log` 后GREEN `/tmp/router-radio-support-green.log`。当前仅单元夹具证明解析约定，尚未连接manager/API或真实hwsim输出；应用门禁仍保留。

执行器预检接线：在generation创建、服务和链路变更之前查询授权接口的PHY、同PHY info及channels，校验PHY身份和完整能力；恢复路径同样经过prepare。RED `/tmp/router-radio-preflight-red.log` 证明旧门禁未接通，GREEN `/tmp/router-radio-preflight-green.log` 通过实际Apply路径mock能力允许/受限子信道拒绝/缺观察拒绝/旧安全渲染回归。临时静态拒绝门禁已由实际预检替换。全量Go日志 `/tmp/router-radio-preflight-full-go.log` 运行中；真实hwsim、API/UI仍未验证，不将G06标记为完成。若当前监管域尚未允许目标信道，预检会明确拒绝；集成方须先完成可信监管域准备，不能借hostapd未来可能设置country跳过当前发射限制。

API/UI更新：无线能力API改为同一解析器，增加radioBands与每信道widths/restricted字段，保留原bands/channels兼容结构；查询失败明确unavailable。RED `/tmp/router-radio-api-red.log`，GREEN `/tmp/router-radio-api-green.log`。页面新增标准/带宽，数字选项以number保存，两份app.js同步；新增浏览器RED `/tmp/router-radio-web-red.log` 后GREEN `/tmp/router-radio-web-green.log`，完整47/47退出0 `/tmp/router-radio-web-full.log` 75.0s。执行器接线全量Go67652退出0 `/tmp/router-radio-preflight-full-go.log`；后续API全量重跑 `/tmp/router-radio-api-full-go.log`。

新check vm-managed-wifi-radio复用隔离routed/WPA3故障及恢复场景，配置wifi7/5GHz149/80MHz；测试VM内可信监管域初始化US，打印实际iw能力，验证supplicant wifi_generation和AP实际宽度。Nix会话82690正在构建，日志 `/tmp/router-radio-vm.log`，制品 `/tmp/router-radio-vm`；尚无实测通过结论。

实测发现与修复：初次VM82690在脚本类型检查因literal非空条件报警退出1，改用len判断后VM24495进入运行；真实iw将info频率输出为5745.0 MHz，整数解析导致API明确unavailable并失败。补充decimal RED `/tmp/router-radio-decimal-red.log`，严格仅接受零小数MHz，GREEN `/tmp/router-radio-decimal-green.log`。同时按内核Band1/2/4身份归类，防止5GHz扩展禁用5925MHz与6GHz误合并；边界回归 `/tmp/router-radio-decimal-final.log`。新VM73532构建中 `/tmp/router-radio-vm-decimal.log`；不将此前失败算通过。

旧能力声明修正：health profile更新managed-router，独立WiFiLAN与IPv6 slaac/dhcpv6配置能力明确，移除旧IPv6 unsupported；不宣称当前联网或实体硬件验收。接口RED/GREEN `/tmp/router-health-dual-{red,green}.log`，页面RED/GREEN `/tmp/router-health-web-{red,green}.log`；兼容旧profile显示。最新全量Go94052退出0 `/tmp/router-radio-api-full-go.log`，但早于health/decimal修复，最终仍须复验。

真实观察二次诊断：VM73532退出1仍为能力解析不可用。离线还原完整info/channels到 `/tmp/router-radio-real-{info,channels}.txt`，确定未支持S1G(Band5)整数MHz重复触发重复检查。忽略未知内核频段，保持目标频段重复拒绝；RED/GREEN `/tmp/router-radio-s1g-{red,green}.log`。完整真实观察成功解析113个目标频段信道，真实观察wifi7/149/80请求判定 `/tmp/router-radio-real-request.log` 通过；临时绝对路径诊断测试已移除，持久边界回归保留。信道本身新增Band字段，API标签与执行器同用内核频段，避免5925边界误标。新VM50395运行中 `/tmp/router-radio-vm-s1g.log`，尚未成功结论。

完整Go41712退出0 `/tmp/router-radio-band-full-go.log` managed13.666/server36.200s（早于S1G修复）；完整浏览器74447退出0，48/48 `/tmp/router-radio-health-web-full.log`80.95s。

VM50395实测已通过AP启用、错误密码拒绝、正确SAE关联（wifi_generation=7及实际80MHz检查）、DHCP、两侧真实DNS/NAT，以及管理/跨网段授权撤销；完整生命周期仍运行，不能当终态成功。另新增vm-managed-wifi-he（wifi6/80MHz）和vm-managed-ipv6-dual-radio（wifi7/80MHz+真实双网PD/过期/变更/重启）组合检查；后者22384构建中 `/tmp/router-dual-radio-vm.log`，制品 `/tmp/router-dual-radio-vm`。竞态38744退出0 `/tmp/router-radio-runtime-race.log` managed14.560/config/wifi通过；vet38690退出0 `/tmp/router-radio-runtime-vet.log`。

WiFi7完整VM50395终态退出0，`/tmp/router-radio-vm-s1g.log`332.75s；制品 `/tmp/router-radio-vm` => `/nix/store/50ja4aqj8wij6axic6glhibbpkxy1jf5-vm-test-run-managed-wifi-wpa3-sae-routed-wifi7-80`。含generation7/实际80MHz、错误密码拒绝、DHCP/DNS/NAT、权限撤销、WAN filter关闭仍隔离、AP失败恢复、关闭、last-good恢复、整机重启44.66s、桥接切换42.41s。当前组合双IPv6 VM22384仍运行，HE独立VM开始 `/tmp/router-he-vm.log`。

HE VM25423终态退出0，`/tmp/router-he-vm.log`326.88s，制品 `/tmp/router-he-vm` => `/nix/store/2hxdnmc7xr5gd4v54c3vgd1alaxsk90c-vm-test-run-managed-wifi-wpa3-sae-routed-wifi6-80`；独立HE协商及实际80MHz、完整IPv4生命周期通过。双IPv6+EHT组合22384终态退出0，`/tmp/router-dual-radio-vm.log`367.94s，制品 `/tmp/router-dual-radio-vm` => `/nix/store/qbxl6fjpbamlqh38aj7pv4ndm6v438hx-vm-test-run-vm-managed-ipv6-dual-radio`，含真实PD变更/容量/自然过期/重启/关闭全链路。均为开发快照，最终候选版本仍需复验。
