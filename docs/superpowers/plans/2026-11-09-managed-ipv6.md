# Managed IPv6 Implementation Plan

> 使用 superpowers:executing-plans 自行实施；全部任务完成后仅一次独立整变更审阅。

**Goal:** 实现独立管理器的 WAN IPv6 获取、PD、LAN 通告及双栈转发，并验证更新、失效和停用清理。
**Architecture:** 专属 odhcp6c 接收 WAN RA/DHCPv6；受限 Go 回调解析事件并管理当前 generation 的地址、路由和 LAN 前缀。继续使用专属 dnsmasq 提供 LAN DNS/RA/DHCPv6，不借用 networkd/NetworkManager/dhcpcd，不引入 NAT66。
**Tech Stack:** Go net/netip、odhcp6c、pppd IPv6CP、dnsmasq、nftables、NixOS 多节点 VM。
**Spec:** ../specs/2026-10-15-usable-home-router-design.md 第3–8节，尤其 A05–A08/A10。

## Global Constraints

- 不操作实体 gw 或宿主网络，不合并；真实网口和运营商配置由 nixos-home 集成。
- 保留网络管理器互斥、root 执行器/低权限 API 和 revision 保存契约。
- 不增加 LAN 改址倒计时回滚；IPv6 应用失败仍遵循现有 last-good 恢复。
- Web 双份资产同步；真实流量及撤销证明前不开放 managed IPv6 能力声明。
- 本计划是已批准完整家用路由设计的实施拆分，不缩小后续访客/多AP/高级模块范围。

## Review Focus

- 恶意或格式错误的 RA/DHCPv6 数据不能变成命令、路径或任意接口操作；错误不回显网络输入。
- PD /56、/60、/64 与 Prefix Exclude 均须正确；不能把排除段分配给 LAN，不能把 /80 冒充 /64。
- preferred=0 的前缀仍可能有效；valid=0 是撤销，不能被忽略或重新赋予默认寿命。
- 旧 generation、重排及重复回调不能污染新配置；前缀更换、断线与关闭不能留默认路由/通告/旧 DNS。
- 允许必要 ICMPv6/ND/DHCPv6，仍拒绝 WAN 管理与非请求入站；IPv4 不能因 IPv6 失败而被误清理。

## Task 1: PD 事件及 LAN 子网选择的纯契约

Files: `backend/internal/managed/ipv6_prefix.go`、`ipv6_prefix_test.go`。
Interfaces: `parseDelegatedPrefixes(string) ([]ipv6Delegation, error)`；`ipv6Delegation.lanPrefix() (netip.Prefix, error)`。
- [x] RED：旧三字段和新五字段（额外 T1/T2）事件、十六进制 class、excluded、无限/撤销/弃用寿命；输入上限、非法地址/长度、重复冲突及安全错误。
- [x] 严格解析网络前缀和数值；不运行命令。保留弃用与撤销状态；选择不与 excluded 重叠的第一个 /64，无法分配返回明确错误。
- [x] 定向 Go 测试/race、managed 回归通过后提交；此时 managed 仍拒绝 IPv6，不宣称运行支持。

## Task 2: 完整协议事件和生命周期

Files: `backend/internal/managed/ipv6_event.go` / tests、`ipv6_runtime.go` / tests、受限回调命令入口、manager.go、modules/managed.nix。
- [x] 解析 ADDRESSES/RA_ADDRESSES/RA_ROUTES/RDNSS/RA_DNS/RA_MTU，明确时间单位、失效及输入上限；禁止 IPv4、任意 zone 和 shell 文本。
- [ ] generation/接口绑定、root 私有状态与回调串行化；只维护授权资源，显式记录已安装状态，不能宽泛 flush 非归属路由。
- [ ] odhcp6c 启停、SLAAC/IA_NA/PD 获取、更新与撤销；寿命交给内核或有界到期调度；DNS 与 IPv4 独立文件再统一提供 dnsmasq。
- [ ] PPP IPv6CP、PPP 断线/重连联动和 IPv6 MSS 上限；不把 RA 默认路由或 IPv6CP 完成误认为互联网可达。
- [ ] 失败注入、重复事件、重启恢复及旧 generation 回放 RED→GREEN。

## Task 3: LAN、配置与 Web/API

Files: config IPv6 验证、managed render、防火墙、dnsmasq、服务状态、双份 app.js、浏览器用例。
- [ ] 明确现有 wanMode/PD/RA 组合；SLAAC 不隐式提供可路由 LAN 前缀；PPPoE6 必须绑定 PPP WAN。保留禁用草稿字段，不静默改意图。
- [ ] delegated /64 配置 LAN 地址，RA 与 DHCPv6/DNS 随配置启停，通告寿命不超过上游；前缀更新/失效撤销旧通告和地址。
- [ ] IPv6 forwarding、必要控制报文、LAN-only 管理、WAN 未请求入站阻断；不配置 NAT66。
- [ ] 状态区分地址/前缀/默认路由/等待与不可用；页面反映真实支持组合并保留结构化字段错误。
- [ ] 配置、事件和浏览器用例 RED→GREEN，全量 Go/vet/race、模块评估。

## Task 4: 真实协议验收及提交

Files: `tests/vm-managed-ipv6.nix`、`tests/managed-ipv6.py`、flake/CI、测试及实现状态文档。
- [ ] ISP RA + DHCPv6/PD，LAN client 获取地址/默认路由/DNS并进行真实双栈流量；规则由 routerd 应用。
- [ ] WAN IPv6 Web/SSH 与 unsolicited forwarding 拒绝，必要 ICMPv6/PMTU 及 LAN 管理可用。
- [ ] PD 变更/撤销、上游失联、无 PD、SLAAC-only、IPv6 关闭/重开、进程与整机重启、PPPoE6 场景。
- [ ] 全量回归和一次独立审阅；修复 Important 后记录具体制品/日志，提交，不将 VM 等同实体 gw 验收。

## Rulings / ledger

- 基线 `9c827b8`，隔离 worktree `/tmp/nixos-router-gw-check-20261029`，分支 `feat/20261109-managed-ipv6`。原工作区用户修改保持不动。
- odhcp6c 是协议客户端；不用其示例脚本直接覆盖全局 resolv.conf 或全接口 flush。回调必须受 managed generation 和资源归属约束。
- 上游 README 描述三字段地址/前缀，当前 script.c 实际还输出 T1/T2；解析器须同时兼容并测试，不能只按 README 猜格式。参考：https://github.com/openwrt/odhcp6c/blob/master/README.md 和 https://github.com/openwrt/odhcp6c/blob/master/src/script.c 。锁定 nixpkgs 客户端版本评估句柄96072待查询。
- PD 初始单LAN使用首个可用 /64，考虑 excluded 段；将来多网段须稳定分配 subnet ID，不能因扩容重排现有LAN。当前不提前实现访客网络。

- Task1 RED：/tmp/router-ipv6-prefix-red.log 编译报告 parseDelegatedPrefixes 未定义。GREEN：/tmp/router-ipv6-prefix-green.log 定向0.010s；完整managed race /tmp/router-ipv6-prefix-race.log 5.598s退出0；vet/diff检查通过。
- 解析支持三字段及带T1/T2的五字段、十六进制class、单个excluded；输入64KiB/128项上限，重复冲突整事件拒绝，错误不回显网络输入。保留valid=0撤销记录和preferred=0弃用记录；/64选择不遍历大地址空间，排除更小子段时跳过整个/64。
- nixpkgs评估96072退出0：odhcp6c版本0-unstable-2026-06-27，src=/nix/store/6h5dmzqh1c8as4n063zmc1zda94riqf1-odhcp6c-10a5222。此source尚未realize，本轮协议格式核对来自上游README/script.c；接入前仍需核对锁定版本实际输出/参数。
- 当前仅纯函数及测试，无命令执行、无配置能力放开、无网络部署。Task2–4尚未完成；下一步处理地址/RA路由/DNS事件解析及受限回调生命周期，不能跳过更新、撤销、寿命及旧generation测试。

- 2026-11-10 Task2协议解析部分完成，生命周期尚未接入。锁定客户端源码通过临时GC root /tmp/router-ipv6-odhcp6c-source保留（首次无root构建后路径已消失；86580已退出0）。src/script.c已核对三/五字段、RA_DNS纯地址、RA_ROUTES四字段及有效期0撤销；odhcp6c.c已核对unbound发生在清除IA状态之前，RDNSS可能仍旧，而RA仍有用，运行态必须按来源和事件清理。
- 新增ipv6_event.go/test：保留DHCP/RA地址及DNS来源，保留地址host bits、零寿命、无限寿命和多个网关；整个事件64KiB，每列表128项；冲突重复整事件拒绝；路由网关仅允许无zone的link-local地址，DNS允许global/link-local但不能loopback/unspecified/multicast/IPv4；未知环境项不参与执行。
- RED /tmp/router-ipv6-event-red.log：未定义parseIPv6Event；GREEN /tmp/router-ipv6-event-green.log：定向0.005s。额外边界RED /tmp/router-ipv6-event-network-red.log证明地址2001:db8::1/0被错误接受；修复同时验证所属网络，保持host bits不变。最终完整managed race55926退出0，5.485s（/tmp/router-ipv6-event-final-race.log），vet/diff退出0。
- 下一步是Task2运行态：generation内还没有回调所需的受限上下文，需要渲染独立IPv6上下文和环境（不复制PPP凭据），记录已安装资源，回调在私有锁内再次验证current generation/接口；manager先停止协议服务，再锁定清理/切换，避免systemctl等待回调时持锁死锁。unbound清DHCP来源，保留仍有效RA；stopped清全部本代IPv6资源。不得只完成解析就宣称IPv6可用。

- 2026-11-10 Task2继续：ipv6_lease.go实现协议来源状态转换与保守重放寿命；unbound清DHCP地址/PD/DNS保留RA，started/stopped清全部；RA合并通知含完整地址/PD可恢复绑定标记，单独旧RDNSS不够。重放扣除向上取整的已过秒数，保留无限寿命和撤销项；旧时间观察拒绝。DNS导出没有TTL，重放不发明DNS寿命，应等待新协议回调（at仅用于重放，不等价新回调的DNS处理）。资源是否已安装必须由另一个journal表示，不能把lease当作成功执行证明。
- 新增ipv6_generation.go与测试：候选目录生成不含秘密的ipv6-context.json；回调通过当前代次/物理接口allowlist/实际WAN/桥名检查，在私有flock内运行。上下文拒绝symlink/hardlink/非0600/非当前进程所有者/超16KiB；代次目录拒绝symlink及非私有目录。root入口仍必须先调用现有privsep状态祖先校验，不将非root单元测试视为root边界已接入。
- switchFiles已实际接入同一锁，等待上限20秒且支持调用方取消；不会在systemctl stop时持锁。并发测试证明回调持锁时切换超时、释放后切换成功、旧回调不执行。未添加协议服务或开放IPv6能力。
- RED：/tmp/router-ipv6-lease-red.log及/router-ipv6-generation-red.log（后者完整路径/tmp/router-ipv6-generation-red.log）观察缺实现；/tmp/router-ipv6-lease-order-red.log观察旧事件覆盖新状态。定向GREEN及完整managed回归4.074s；最终managed race29185退出0，7.910s（/tmp/router-ipv6-generation-final2-race.log），vet/diff通过。最终全量Go30977运行中，日志/tmp/router-ipv6-generation-final-go.log；先poll，不重复启动。
- 下一步必须接实际资源journal/命令reconcile：写前记录旧+候选资源以容忍回调SIGTERM，失败不宣称已安装，按精确资源撤销而非全接口flush；generation切换在锁内先清理已记录IPv6资源。然后接root回调入口、专属odhcp6c/Nix服务。仍需LAN通告、防火墙与真实VM，不再仅凭解析或锁测试宣称Task2完成。

- 最终全量Go30977已终态退出0（/tmp/router-ipv6-generation-final-go.log）。本轮生命周期/代次保护基础可提交，Task2资源命令和服务接线仍未完成；尚未为此基础变更运行VM。当前无存活测试进程。
