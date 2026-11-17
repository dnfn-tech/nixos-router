# Managed IPv6 Implementation Plan

> 使用 superpowers:executing-plans 自行实施；全部任务完成后仅一次独立整变更审阅。

**Goal:** 实现独立管理器的 WAN IPv6 获取、PD、LAN 通告及双栈转发，并验证更新、失效和停用清理。
**近期里程碑（2026-11-12 用户确认）：** 本计划服务于 [gw 集成软件候选目标](../specs/2026-11-12-gw-integration-goal.md)。IPv6 收尾与双网段、WPA3/WiFi 6/7 通用能力按 gw 功能对照表排定优先级；其他完整家用路由扩展保留为后续事项。
**Architecture:** 专属 odhcp6c 接收 WAN RA/DHCPv6；受限 Go 回调解析事件并管理当前 generation 的地址、路由和 LAN 前缀。继续使用专属 dnsmasq 提供 LAN DNS/RA/DHCPv6，不借用 networkd/NetworkManager/dhcpcd，不引入 NAT66。
**Tech Stack:** Go net/netip、odhcp6c、pppd IPv6CP、dnsmasq、nftables、NixOS 多节点 VM。
**Spec:** ../specs/2026-10-15-usable-home-router-design.md 第3–8节，尤其 A05–A08/A10。

## Global Constraints

- 不操作实体 gw 或宿主网络，不合并；真实网口和运营商配置由 nixos-home 集成。
- 保留网络管理器互斥、root 执行器/低权限 API 和 revision 保存契约。
- 不增加 LAN 改址倒计时回滚；IPv6 应用失败仍遵循现有 last-good 恢复。
- Web 双份资产同步；真实流量及撤销证明前不开放 managed IPv6 能力声明。
- 本计划是家用路由的 IPv6 实施拆分；2026-11-12 起近期完成标准按 gw 集成软件候选目标执行，访客/多AP/高级模块继续保留在长期清单。

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

- 2026-11-10 Task2资源层：新增私有写前journal，精确地址/路由安装和撤销，识别外部资源冲突；失败保留旧与候选资源以支持重试。switchFiles在代次锁内先清理旧IPv6资源，失败不切换current；DNS写入独立wan6-resolv.conf。此入口尚未连接协议服务。
- 无可用LAN /64的合法委派不阻断完整事件：撤销旧LAN地址和路由，同时保留WAN更新及其他可用委派。新增三种回归均先失败再通过（/80、排除覆盖整个/64、多委派混合）。
- 真实用户/网络命名空间验证发现并修复两个fake未覆盖的问题：地址协议242在iproute2 JSON中为"0xf2"，以及host route目标省略/128。新增ROUTERD_TEST_IPV6_KERNEL=1可选回归，测试自行unshare并验证网络命名空间已隔离，覆盖真实安装、重复更新、停止撤销及外部地址保留；8482退出0，0.402s。没有操作宿主或gw网络。该测试不等于odhcp6c/PD/LAN完整VM验收。
- 继续项：回调排队/命令期间寿命扣减，root回调入口与odhcp6c服务接线，LAN通告与防火墙、完整VM。IPv6能力仍关闭，Task2尚未完成。

- 2026-11-10继续：每个IPv6安装命令发出前按观察时间扣减向上取整的已过秒数；排队/前序命令导致过期时不安装，并撤销同slot旧资源，保留无限寿命。Clock依赖默认time.Now，测试用可控时钟覆盖排队3秒、前一命令5秒、全部过期及旧资源清理；观察时间不合法/时钟倒退拒绝。协议观察journal仍保留原始时间与快照，安装inventory单独记录。
- 新增router-executor ipv6-event子命令和managed.HandleIPv6Event：仅root调用，先CheckState再New；只读取7个协议环境字段，参数没有命令执行透传；代次/接口保护复用现有锁。捕获启动时间用于寿命扣减，整体45秒上限，SIGTERM/INT取消。参数非法、协议非法和外部接口在执行前拒绝。尚未配置Nix服务调用，因此不是完整运行验收。
- 本轮验证：全量Go33569退出0（/tmp/router-ipv6-callback-go.log）；managed race8.995s；执行器race首次TestNotifyReady受沙箱Unix socket限制失败，另在正常权限环境重跑。隔离真实内核及资源/回调用例40935退出0，1.228s（/tmp/router-ipv6-callback-kernel.log）；vet及diff check通过。下一步接odhcp6c服务与代次环境、停止/切换联动，随后LAN通告/防火墙和完整VM；不能提前声明Task2完成。
- 执行器race正常环境重跑61442退出0，1.033s（/tmp/router-ipv6-callback-executor-race.log）；当前无存活测试进程。本轮资源层及回调代码仍未提交。

- 2026-11-10服务接线：每个候选写入私有wan6.env，绑定实际WAN/模式/PD/代次；独立nixos-router-wan6服务运行锁定odhcp6c，dhcpv6/pppoe6使用-S -N try，slaac使用-N none，PD按配置-P 0。协议hook调用受限ipv6-event入口，无通用命令透传；ExecStopPost在旧进程组退出后补发stopped清理，Restart=always适应接口消失/重建，私有/run目录存放PID。
- activate先停wan6再停PPP等服务、锁内清理旧IPv6资源并切换代次，启用时启动wan6；首次应用失败cleanup也在协议服务停止后锁内精确清理，停止失败不继续拆网络。公开validate/capability仍未开放IPv6，PPPoE IPv6CP、LAN/防火墙/真实协议验收仍待完成。
- 新增Go环境实际shell读取及启停顺序用例，RED缺失wan6.env，GREEN通过。新增tests/wan6-service.nix执行实际生成的启动/hook脚本（仅协议客户端与执行器为参数记录替身），验证DHCPv6/SLAAC参数、代次透传和外部接口/模式拒绝；34436退出0，制品/tmp/router-wan6-service-contract，日志/tmp/router-wan6-service-contract.log。锁定客户端制品/tmp/router-ipv6-odhcp6c已构建，确认bin/odhcp6c存在。
- 模块评估14170退出0全部true（/tmp/router-ipv6-service-module.json）；全量Go91852退出0（/tmp/router-ipv6-service-go.log），managed race9.644s，vet/diff通过。现有有线VM86374已启动，日志/tmp/router-ipv6-service-vm.log，输出链接/tmp/router-ipv6-service-vm；先poll该句柄，不因观察超时重启。此VM是IPv4回归，不是完整IPv6验收。

- PPP IPv6CP接线：仅IPv6.enable且wanMode=pppoe6渲染+ipv6，否则保留noipv6。pppoe6不在activate提前启动odhcp6c，由IPv6CP up hook启动；down hook停止，PPP ExecStopPost补偿进程崩溃。hook受当前代次保护。
- 核对锁定pppd2.5.2源码（GC root /tmp/router-ipv6-ppp-source）：lcp_up先ppp_set_mtu再link_established，PHASE_NETWORK同步运行net-pre-up（ipparam为第4参数），因此将MSS更新从ip-pre-up迁移到net-pre-up；IPv4/IPv6分别使用MTU-40/MTU-60，初始规则1452/1432按nfproto隔离。仍须重新运行低MRU真实PPP流量，未用纯规则测试替代验收。
- 源码还证明pppd run_program以独立script_env执行hook，不继承systemd环境。脚本测试先复现没有启动wan6（98807退出1，/tmp/router-ppp6-env-red.log），修复为当前代次校验后读取该代私有wan6.env。随后一次测试受固定/tmp夹具残留影响失败，夹具改为按源码散列命名、排他创建并trap清理；55320退出0（/tmp/router-ppp6-env-green2.log，/tmp/router-ppp6-service-contract）。覆盖IPv6CP up/down、旧代次忽略、PPP退出清理、关闭IPv6不误停客户端。
- 全量Go79415退出0（/tmp/router-ppp6-go.log），managed race9.140s（/tmp/router-ppp6-race.log），vet/diff通过。模块56103待poll；原有线VM86374仍存活，依赖cache.nixos.org下载出现低速重试，未达到测试执行阶段。该VM快照早于本轮PPP更改。下一步继续poll现有句柄、运行最新PPP低MRU回归，再完成LAN/防火墙与真实IPv6VM。公开IPv6能力仍关闭。
- 模块56103已poll终态退出0，/tmp/router-ppp6-module.json全部true。原VM86374仍存活，保留句柄继续观察。

- IPv6配置矩阵补齐：启用时要求有效WAN模式，pppoe6与PPP WAN互相匹配，SLAAC不能委派前缀，LAN RA依赖LAN PD；错误为准确字段路径。关闭时保留已知模式/PD/RA草稿，managed不再因关闭的PD/RA字段阻断IPv4应用，但Enable仍受运行能力门禁。RED已观察七种非法组合被接受，以及关闭草稿阻断IPv4应用；修复后通过。
- 配置阶段验证：全量Go55895退出0（/tmp/router-ipv6-config-go.log），config race1.144s、managed race8.978s（/tmp/router-ipv6-config-race.log），vet/diff通过。公开IPv6能力仍关闭。
- LAN RA接入前已获取锁定dnsmasq2.93源码（/tmp/router-ipv6-dnsmasq-source，解包/tmp/router-dnsmasq-inspect/dnsmasq-2.93）及man。constructor读取内核地址寿命，旧前缀有专门弃用路径；多resolv-file不是合并而是采用最近修改文件，不能简单同时指定IPv4/IPv6文件。隔离用户/网络命名空间自然到期抓包47481退出0，8秒前缀的PIO递减到valid=0/preferred=0。提前删除试验发现路由端内核自动SLAAC生成同前缀地址，需关闭授权接口accept_ra/autoconf后才能判断资源撤销；内核证据/tmp/router-ra-withdraw-long-probe.log。下一步检查修正后的34859结果，不从有干扰的试验下结论。
- 34859已退出0（/tmp/router-ra-withdraw-controlled-probe.log）：关闭隔离路由端accept_ra/autoconf后，2.04秒删除全局地址，ip JSON确认只剩link-local；2.05秒RA为preferred=0/valid=59，随后递减。即dnsmasq旧前缀是弃用而非立即零valid撤销，saved_valid从最后一次广告采样，在删除时重新计时，可能延长原始绝对到期。来源：src/dhcp6.c:833开始CONTEXT_OLD转换，src/radv.c:324开始旧前缀通告。须在LAN接入时明确解决，不声称默认constructor满足所有撤销/寿命要求。当前只完成配置约束与实证核查，尚未渲染LAN RA配置。

- RA寿命修正与真实报文验证：新增独立managed专用dnsmasq派生包及最小源码补丁。构造前缀区分“地址仍在但弃用”和“地址已消失”：前者采用内核剩余valid，后者通告零valid；枚举失败不能当成撤销。旧上下文保留60秒发送零寿命通告，并防止valid减法下溢。只替换独立管理器的校验/服务二进制，不替换全局dnsmasq。
- tests/ipv6-ra-lifetimes.py在临时用户/网络命名空间抓真实ICMPv6报文；拒绝在父命名空间创建网络资源。stock包提前撤销RED（/tmp/router-ra-regression-red.log）；第一版修正虽通过主动请求，却在停止RS后无主动撤销，强化用例RED（/tmp/router-ra-unsolicited-red.log），定位为旧上下文在一秒发送定时器前被删除。保留零寿命上下文后12608退出0（/tmp/router-ra-unsolicited-green.log）：2秒删地址，3.024秒收到主动PIO valid/preferred/router全零；自然3秒弃用、8秒到期仍保持剩余有效寿命且未延长绝对截止时间。包制品/tmp/router-managed-dnsmasq。
- 此测试证明报文寿命和主动撤销，不证明客户端立即删除地址（RFC两小时规则）、端到端转发或应用切换时dnsmasq停止前已完成通告。LAN配置、接口sysctl归属、IPv4/IPv6 DNS合并、停止顺序、完整IPv6 VM仍待实现；公开IPv6能力继续关闭。
- 旧IPv4有线VM86374已终止：QEMU缓存低速超时，未执行VM，日志/tmp/router-ipv6-service-vm.log。最新代码重试66955（/tmp/router-ipv6-service-vm-retry.log）尝试禁用HTTP/2，但daemon提示非trusted用户的该设置被忽略，因此实际仍使用默认下载设置；模块及实际生成脚本验证32163（/tmp/router-ra-module.json、/tmp/router-ra-service-contract.log）在运行，须poll各自句柄。没有修改物理gw或宿主网络。

- LAN通告接线：IPv6+PD+LANRA启用时，dnsmasq通过constructor桥名读取实际/64及内核寿命；DNS启用使用ra-stateless（SLAAC及无状态DHCPv6），DNS关闭使用ra-only并显式抑制DNS选项。准备阶段语法检查及activate启动条件包含纯IPv6 RA，不依赖IPv4 DHCP/DNS。关闭IPv6草稿、PD无RA均不误启通告。ipv6_lan_test先RED缺配置（58000），再GREEN；可选ROUTERD_TEST_DNSMASQ验证实际生成配置语法。
- 实际dnsmasq语法+完整managed80362退出0，6.445s；全量Go22556退出0（/tmp/router-ipv6-lan-full-go.log，managed7.418s/server38.314s）；managed race41464退出0，10.024s且vet通过；diff检查通过。模块/协议脚本32163已退出0，/tmp/router-ra-module.json全部true、/tmp/router-ra-service-contract制品完成。
- 停服缺口已复现：79057退出1（/tmp/router-ra-shutdown-red.log），EVENT_TERM直接退出，没有最终RA。专用补丁增加同步ra_shutdown：在退出前将已构造前缀标为OLD且valid0，立即发送通告，同时清零路由/RDNSS/DNSSL寿命；不等待未来timer，不改变manager现有“先停止服务，再拆桥”的顺序。包51847退出0；抓包35536退出0（/tmp/router-ra-shutdown-green.log）：2.029s收到停服零寿命通告，再删地址；主动撤销与自然到期仍通过。后续6235增加RDNSS寿命抓包断言，日志/tmp/router-ra-shutdown-dns-green.log，需poll终态。
- 当前有线VM66955仍运行并下载内核依赖，/tmp/router-ipv6-service-vm-retry.log；快照早于本轮LAN配置与shutdown补丁，不能当作这些改动的完整集成验收。下一步：RDNSS结果、接口sysctl归属、IPv4/IPv6 DNS合并和防火墙，最新真实双栈/PD/PPP6 VM。公开IPv6能力仍关闭，未操作gw。所有本轮新增工作尚未提交。
- RDNSS追加检查6235已退出0，确认停服时DNS寿命为0；但报文暴露非停服撤销/自然弃用时DNS寿命变成4294967295（link-local无限寿命被用作剩余值）。强化每个零router lifetime报文的DNS寿命断言后90663退出1（/tmp/router-ra-dns-withdraw-red.log）。补丁在只有旧前缀、无当前可用前缀时同时清零DNS/DNSSL寿命；30881正在构建及真实报文回归（/tmp/router-managed-dnsmasq-dns-build.log、/tmp/router-ra-dns-withdraw-green.log），须poll，未提前宣称完成。
- 30881退出0，但逐包检查仍发现弃用重构前的一个报文为router=10/DNS=4294967295。将契约增强为DNS寿命不超过同一RA的router lifetime，/tmp/router-ra-dns-bound-red.log退出1；发送DNS选项前统一按该报文router lifetime封顶，避免依赖重构事件处理先后。10075构建/抓包运行中（/tmp/router-managed-dnsmasq-bound-build.log、/tmp/router-ra-dns-bound-green.log）；需poll，不能用上一版GREEN替代最终结果。
- 最终10075已退出0：新包构建与全部三种真实报文场景通过，每个RDNSS寿命均不超过同报文router lifetime，停服和失去可用前缀均归零（/tmp/router-ra-dns-bound-green.log）。尚未验证多前缀共存/新前缀恢复，也未完成整合IPv6流量VM，继续项保持开放。

- 内核自动配置归属：新增claimIPv6Autoconfiguration，仅在已验证/创建归属接口上执行；IPv6启用时在LAN桥、WAN物理接口、LAN成员及启用的WiFi接口上线前关闭accept_ra/autoconf/use_tempaddr/accept_redirects，再启用该接口IPv6。使用slash sysctl路径保留接口名中的点，拒绝all/default/lo及非法名称；不写全局/默认参数，不flush外部IPv6资源。停用后保持自动行为关闭，避免清理后又由内核RA生成无归属状态。IPv6转发参数仍须与防火墙整合验证，尚未开放能力。
- RED83879证明原activate缺参数设置且忽略注入失败；实现后定向GREEN。真实隔离内核测试15199退出0（/tmp/router-ipv6-interface-kernel.log，0.320s），检查全部五项设置及含点接口test.6，并回归地址/路由安装撤销和外部地址保留。
- PPP重连会重建逻辑接口，WAN6每次启动前也需设置相同参数。Nix实际脚本测试22352 RED（/tmp/router-ipv6-interface-service-red.log，缺sysctl调用），增加受接口白名单限制的启动前设置及procps依赖；测试以记录替身执行，不接触宿主sysctl，并验证设置失败不启动客户端。68512脚本构建+模块评估运行中（/tmp/router-ipv6-interface-service-green.log、/tmp/router-ipv6-interface-module.json）。97021全量Go/race/vet运行中（/tmp/router-ipv6-interface-full-go.log、/tmp/router-ipv6-interface-race.log），须poll各自句柄。
- 有线VM66955仍存活，内核缓存低速重试，未达到测试执行；不重复启动，不以下载等待宣称通过。该快照早于本轮变更。下一步继续双栈DNS合并、转发/防火墙及完整IPv6/PD/PPP6 VM；完整家用路由范围不变。
- 97021终态退出0：全量Go（managed7.334s/server36.939s）、managed race9.449s和vet全部通过。68512仍在模块评估，前序脚本制品已构建；先poll，不重复启动。
- DNS接入调查：IPv4 DHCP/PPP hook目前直接写wan-resolv.conf，IPv6写wan6-resolv.conf，dnsmasq只读前者；DHCP deconfig还未清空IPv4 DNS。下一步应将来源更新、代次检查与合并发布放在共享锁内，不能只在IPv6回调拼接两个文件（IPv4后续renew会绕过合并并留下旧结果）。协议停止必须只撤销自己的来源，切换清理两侧来源；静态显式DNS优先关系保持现有configuredUpstreams契约。
- 68512已终态退出0：实际生成脚本与模块评估完成，模块结果/tmp/router-ipv6-interface-module.json。当前仅66955有线VM仍运行。

- 双栈DNS发布接入：新增dns-context.json私有代次上下文（物理WAN、实际WAN、IPv4协议），受限root dns-event入口，IPv4/IPv6来源文件分别wan4-resolv.conf/wan6-resolv.conf，dnsmasq仍只读原子发布的wan-resolv.conf。所有来源读取/验证/更新/合并共用代次锁；切换代次和cleanup锁内清空两侧来源与合并文件，静态显式upstream优先关系不变。源文件限制私有0600/单链接/无symlink/16KiB，拒绝非法地址、注入行、错误地址族、缺少zone的IPv6 link-local，合并去重。IPv4当前接口和模式由私有上下文授权；旧代次不写。
- DHCP bound/renew/deconfig与PPP up/down不再直接写合并文件，统一走受限回调。DHCP ExecStopPost与PPP ExecStopPost补偿异常退出，IPv4-only PPP也清DNS；IPv6现有资源回调发布自己来源，失去一侧保留另一侧。root入口先CheckState再New，无任意命令透传。
- 测试先RED缺HandleIPv4DNS/publishDNSSource（/tmp/router-dns-red.log），定向64591 GREEN；并发来源循环、续租替换、单侧撤销、旧代次/外部接口拒绝、代次切换、坏来源和非法DNS覆盖。实际生成脚本96950 RED缺DNS回调，93775 GREEN（/tmp/router-dns-service-green.log），仅ip/执行器为替身，无宿主网络操作。追加异常退出覆盖89430脚本+模块运行中（/tmp/router-dns-service-final.log、/tmp/router-dns-module.json）。
- 全量Go41159退出0（managed10.291s/server38.989s，/tmp/router-dns-full-go.log）；最新managed/executor race5682退出0（12.942s/1.033s，/tmp/router-dns-race.log）及vet通过，覆盖移除重复锁外DNS清空和并发测试后的代码。diff检查通过。
- 停止预算仍需专门核查：Manager.run为20秒，DNS锁等待20秒、root回调总45秒，DHCP/PPP单位停止预算分别10/15秒；正常脚本测试不是繁忙回调/停止超时证明。须协调预算并验证后才能开放IPv6。原VM66955仍存活在依赖下载重试，快照不含本轮DNS改动，不能算新路径验收。继续转发/防火墙、停止生命周期、真实双栈/PD/PPP6 VM及整体验收。
- 89430已退出0，最终DHCP/PPP异常退出脚本测试和模块评估全部通过（/tmp/router-dns-module.json）。当前只剩旧快照有线VM66955在运行，后续优先处理已确认的停止预算不匹配。

- 停止预算修正：普通命令仍20秒；仅三个受管协议服务（wan/wan6/pppoe）的精确stop/restart命令最多150秒，给systemd进程组停止和ExecStopPost各60秒留余量。调用方更短deadline/取消始终优先，不扩大外部服务或多目标命令预算。WAN和PPP TimeoutStopSec从10/15提升60，与WAN6一致。PPP IPv6-down及ExecStopPost仅排队--no-block stop WAN6，避免在父服务停止中同步等待子服务；Apply本身仍先等待WAN6停止再拆链路/切换代次。总体Apply/恢复时限没有取消。
- 真实耗时回归：ROUTERD_TEST_SLOW_STOP=1运行21秒模拟服务清理，92232 RED在20.02秒错误中断，32942 GREEN在21.036秒完成；无systemd/宿主网络操作。立即取消和预算范围用例20562通过。新增退出回调在stop内实际取得代次锁并发布DNS的测试，72532 race退出0，1.248秒，证明没有持锁等待退出回调的死锁。
- 模块旧时限34865 RED命中WAN<60断言（/tmp/router-stop-module-red.log）。最终实际脚本和模块8876运行中（/tmp/router-stop-service-green.log、/tmp/router-stop-module-green.json），需poll。全量Go/race/vet98232退出0（/tmp/router-stop-full-go.log、/tmp/router-stop-race.log，managed race11.666s/server37.535s），diff通过。21秒测试仅证明管理器等待行为，不替代真实systemd忙碌停止/IPv6流量VM。
- 旧VM66955日志已出现内核缓存下载最终超时和依赖失败，但句柄仍确认存活，可能在等待其他下载退出；不将观察超时当作终态、不重复启动。该旧快照本来不包含最新LAN/DNS/停止修正。下一步完整IPv6转发/防火墙与最新协议VM，最终硬件前提仍未达成。
- 8876已退出0：实际生成脚本（含非阻塞子服务停止）及模块评估全部通过。当前只剩66955旧VM句柄需收取终态。

- IPv6 WAN防火墙接线：实际WAN出口在最终drop前允许链路本地来源、hoplimit255的RA；hoplimit255的NS/NA（包含DAD未指定来源）；必要ICMPv6错误；仅DHCPv6/PPPoE6模式允许IPv6 UDP547→546。PPPoE物理口不额外放行IPv6；IPv4 DHCP规则明确限制地址族。LAN-only管理端口拒绝仍位于协议与established放行之前。既有NAT保持ip saddr限定IPv4，未增加NAT66。
- RED32438证明RA原先被WAN drop截断，GREEN67377通过模式及顺序契约。真实nft语法检查/事务安装52868通过并保留外部sentinel表。新增tests/ipv6-firewall-packets.py由Go父测试创建的隔离用户/网络命名空间调用，二次检查隔离后用AF_PACKET注入真实IPv6帧；初次27785因/sys仍映射父命名空间读不到veth而失败，改用ip -j netlink读取MAC，不挂载/修改宿主sysfs。
- 实际报文16988通过四模式RA/ND/DHCPv6及未请求UDP判定；追加WAN TCP22/8080保护及关闭可选防火墙场景54021通过，再增加普通TCP8081在无过滤时通过的正向对照19411通过（6.946s，/tmp/router-ipv6-firewall-packets-control.log）。反例包括RA错误hoplimit/全局来源，SLAAC不放行DHCPv6；五模式循环重新安装规则且外部表仍存在。该测试验证本机input过滤，不冒充LAN-WAN转发、真实PPP或真实DHCPv6/PD获取验收。
- 全量Go/race/vet87533退出0（/tmp/router-ipv6-firewall-go.log：managed10.710s/server37.184s；/tmp/router-ipv6-firewall-race.log：12.807s）；diff通过。IPv6公开能力仍关闭，内核转发、双栈端到端流量、多前缀更新/恢复及完整协议VM仍待完成。旧VM66955仍以原句柄确认存活，日志已有内核缓存依赖失败，未启动重复构建；不将此当验收通过。

- IPv6转发语义查证：https://docs.kernel.org/networking/ip-sysctl.html 的conf/all/forwarding与force_forwarding说明区分全局转发和单接口实际转发。隔离Linux6.18.46实验21035退出0（/tmp/router-ipv6-forward-probe.log）：全局0，baseline不通，仅接口forwarding=1仍不通，force_forwarding=1后可通，清零再阻断。不借此写宿主全局参数。
- 接入setIPv6Forwarding：只接受合法非all/default/lo接口；启用顺序forwarding=1→force_forwarding=1，不支持该内核能力时明确失败，不回退到全局开关。禁用force=0→forwarding=0，对缺少该knob的旧内核仅禁用路径使用sysctl -e，避免IPv4应用被额外内核要求阻断。activate在firewall安装成功后才开启已委派LAN/WAN转发；PPPoE物理WAN保持关闭，逻辑WAN由每次WAN6启动设置，重连也覆盖。关闭IPv6或PD会清零；cleanup清物理WAN，受管桥删除同时移除其设置，不对外部同名桥写sysctl。
- RED70014缺少转发配置，GREEN26338通过应用/禁用/清理条件。新增TestIPv6ForwardingKernel与tests/ipv6-forwarding-packets.py在隔离命名空间装载实际生成的nft规则、调用生产转发setter，并注入真实IPv6 UDP：LAN→WAN、已建立回程通过；未请求WAN→LAN拒绝；禁用后双向停止；Hop Limit减1、源/目的地址保持（无NAT66）；全局forwarding保持0、external0不改变。84073退出0，2.136s（/tmp/router-forward-kernel.log）。此为合成L3接口转发验证，不冒充真实DHCPv6/PD/PPPoE或完整硬件桥验收。
- PPP/WAN6脚本89359 RED缺force_forwarding后接线，48125最终退出0，实际脚本与模块评估全部通过（/tmp/router-forward-service-green.log、/tmp/router-forward-module.json）。全量Go/race/vet62822退出0（managed10.943s/server36.520s；race12.983s），diff通过。公开IPv6能力继续关闭，完整协议VM、多前缀/恢复与状态UI尚未收尾。
- 旧VM66955本轮已确认终态退出1，依赖内核/模块/QEMU缓存下载失败。HTTP/1.1本地缓存诊断：签名narinfo正常下载，内核15MB对象82626退出0约4.4秒（/tmp/router-vm-http1-download.log）；没有改daemon设置。74682正在并行下载模块与QEMU原始签名缓存到/tmp/router-vm-local-cache（/tmp/router-vm-http1-rest.log）；随后必须nix copy从本地cache导入并验证签名/NAR哈希，不能直接信任curl成功。缓存补齐后启动最新快照的有线和PPP回归，并继续真实IPv6/PD集成。
- 本地签名cache：内核nix copy已退出0（/tmp/router-vm-kernel-import.log），未禁用签名校验。QEMU文件已完整下载；首次递归copy因本地cache没有已存在依赖的narinfo而失败，确认brltty依赖在本机store，改用nix copy --no-recursive保留签名检查，仅导入QEMU；53157待poll（/tmp/router-vm-qemu-import.log）。模块文件仍慢，74682内curl在180秒超时后按已设策略重试，不能把它当作已完成；没有启动重复VM。
- 53157已退出0，QEMU按原始签名和NAR校验成功导入；只剩模块下载74682仍在运行。

- 后续核实：模块签名缓存导入成功；最新有线与PPP VM均生成成功制品（/tmp/router-protocol-regression及-1），日志/tmp/router-protocol-regression.log分别记录210.20s、105.60s完成。真实RA多前缀弃用、撤销和恢复测试通过（/tmp/router-ra-renumber.log），不代替客户端协议验收。
- 新增tests/vm-managed-ipv6.nix及managed-ipv6.py，使用真实Kea IA_NA/PD、radvd、API应用与客户端IPv6/DNS/无NAT66流量，覆盖重复应用、旧代次拒绝与关闭清理。Nix评估成功；首次完整VM构建65714运行中，日志/tmp/router-ipv6-vm-red.log。运行能力门禁仍关闭，预计先收集端到端失败证据；尚未宣称IPv6验收。

- 公共执行入口接通：TestIPv6ApplyAndReloadLastGood首先RED（32043，三种模式均被旧unsupported门禁拒绝），移除validate中IPv6统一拒绝，保留配置矩阵及接口归属验证；负例改为非法WAN6模式。92367定向GREEN；全量Go86156已终态退出0，managed14.604s/server42.496s（/tmp/router-ipv6-gate-go.log）。新增用例验证Apply保存后New能重新加载启用IPv6的last-good，防止root回调构造管理器失败。Web capability仍未开放，协议验收未完成。首次VM65714是门禁修正前快照，仍需收取结果后串行跑新快照。

- 验收补强：managed-ipv6.py显式要求WAN地址属于Kea IA_NA地址池，避免仅SLAAC成功掩盖DHCPv6失败；Python语法检查通过。gate后race日志/tmp/router-ipv6-gate-race.log记录managed13.082s和executor通过。源码另确认RA_MTU当前仅解析未应用，须接入并用PMTU真实流量验证，不能算协议完整。
- VM65714仍在等待Kea依赖：官方cache的kea及doc尚未入store。HTTP/1.1补缓存46279（/tmp/router-kea-http1.log）doc完整，主包仍下载；正常签名校验的doc导入93865（/tmp/router-kea-doc-import.log）仍待收取。原构建保持运行，不重复启动；待终态后运行最新门禁及IA_NA断言快照。

- Kea缓存已补齐：HTTP1下载46279退出0（主包首个180秒超时后自动重试成功）；doc签名导入93865退出0。主包导入72957被原VM下载daemon阻塞，lslocks确认PID555723 WRITE*的blocker为519298、同inode43313791。显式SIGINT仅停止本任务旧快照nix客户端519284，65714退出1（未执行VM，不计测试失败）；释放锁后72957立即退出0，保留全部签名/哈希校验。随后串行启动最新VM54701，日志/tmp/router-ipv6-vm-current.log，输出/tmp/router-ipv6-vm-current；当前编译阶段。
- MTU输入边界：锁定odhcp6c ra_set_mtu只接受1280..65535，当前解析器此前接受65536。扩充边界负例与0/1280/65535正例，31637观察RED，修复上限后定向IPv6Event测试通过。全量Go57202终态退出0（managed9.188s，/tmp/router-ipv6-mtu-bounds-go.log）；diff check通过。此修正不代替RA MTU运行态接入/PMTU验收。

- 首次实际IPv6 VM54701终态退出1：API Apply与WAN6服务启动已通过，90秒IA_NA等待失败。ISP日志明确Kea在link-local DAD未完成时bind547失败，默认max-retries=0后仍报告服务started但无socket；不将此误判为路由器DHCP缺陷。按锁定Kea3.2.0文档给测试ISP配置require-all、20次/1000ms重试，并在脚本先验证ss UDP547监听；IA_NA失败时输出两侧协议日志/地址路由。Python/Nix语法与diff检查通过，重跑43794（/tmp/router-ipv6-vm-kea-ready.log，输出/tmp/router-ipv6-vm-kea-ready）运行中。
- MTU机制验证：官方内核文档https://docs.kernel.org/networking/ip-sysctl.html说明IPv6独立接口mtu及accept_ra_mtu。临时隔离实验/tmp/router-ipv6-mtu-probe.py证明IPv6 socket路径MTU1500→1400→1500，而物理link MTU始终1500；首次因Python未暴露IPV6_MTU常量失败，改Linux选项24后退出0。仅机制验证，尚未接资源journal、停用恢复或实际转发PTB；不得据此称PMTU完成。

- 43794终态退出1：Kea UDP547已就绪，SOLICIT/ADVERTISE及后续INFORMATION_REQUEST可见，但无IA_NA/PD。新增诊断证明root回调正常安装WAN SLAAC /128、proto242 on-link和default路由，没有资源冲突错误。锁定Kea文档9.2.21说明直连IPv6子网使用interface选择；当前测试子网未绑定接口，补interface=eth1作为单变量验证，不改路由器客户端参数。最新VM65235运行中（/tmp/router-ipv6-vm-subnet.log，输出/tmp/router-ipv6-vm-subnet）；尚未确认假设解决。

- VM65235终态退出1，子网绑定假设得到验证：真实IA_NA2001:db8:1::100、IA_PD2001:db8:100::/60、router LAN地址与default(proto242)、LAN客户端SLAAC均通过，双栈DNS来源已加载；失败推进到client→2001:db8:ffff::1 ping超时。增加三节点地址/路由/邻居、router nft与force_forwarding诊断，不先猜测修改转发规则。最新诊断VM3767运行中（/tmp/router-ipv6-vm-forward-debug.log，输出/tmp/router-ipv6-vm-forward-debug）。
- 集成归属补齐：modules/managed.nix拒绝受管wired/WiFi接口上的NixOS IPv6地址、已有Kea DHCPv6及radvd服务；未受管接口与preview保留。新增针对本模块具体断言的负例及兼容正例，56636 RED缺DHCP6拒绝。第一次全量65466失败在preview夹具缺Kea必需settings，与产品断言无关；补settings={}后全量模块50376运行中，日志/tmp/router-ipv6-ownership-green2.log及json。diff检查通过，不能提前宣称全量评估通过。

- 模块50376终态退出0，/tmp/router-ipv6-ownership-green2.json全部true，含IPv6 wired/WiFi冲突、Kea6/radvd拒绝、外部接口与preview兼容。转发测试改用真实bridge+lan0成员代替命名br-lan的veth，57632退出0（1.949s，/tmp/router-ipv6-bridge-forward.log），覆盖双向已建立流量、未请求拒绝、禁用及外部接口不变。
- 诊断VM3767终态退出1，已定位流量到达ISP：ping返回From 2001:db8:1::1 Destination unreachable No route；三节点路由、force_forwarding均正确，ISP实际lo只有::1，缺2001:db8:ffff::1。核对锁定nixpkgs network-interfaces-scripted.nix：lo的deviceDependency为空、非container且非defaultgateway时地址服务wantedBy为空。测试ISP显式将network-addresses-lo挂multi-user.target，并先wait unit/验证目标/128。最新VM85146运行中，日志/tmp/router-ipv6-vm-endpoint.log，输出/tmp/router-ipv6-vm-endpoint；不修改生产转发规则掩盖夹具错误。

- 基础真实IPv6 VM85146终态退出0，test script83.68s；制品/tmp/router-ipv6-vm-endpoint -> /nix/store/gq0j21lx20synar7r9ya9gfpz2l1s8gz-vm-test-run-vm-managed-ipv6，日志/tmp/router-ipv6-vm-endpoint.log。通过API Apply获取真实IA_NA/PD、client地址/default及双栈ping、AAAA查询与双源DNS发布、IPv6HTTP原始PD源地址（无NAT66）、WAN Web/SSH拒绝、重复Apply后流量、旧代次回调不改DNS、关闭IPv6清理地址/proto242路由/DNS/转发且IPv4仍可用。只代表此测试覆盖，不替代PPP6/重启/变更/PMTU/浏览器完整验收。
- 已新增真实上游DHCP6停止/租约自然到期/RA及IPv4保留/重新启动恢复子测试，不用手工事件代替协议计时器。最新79140运行中（/tmp/router-ipv6-vm-lease-loss.log，输出/tmp/router-ipv6-vm-lease-loss），尚未宣称通过。

- 79140租约生命周期VM终态退出0：全脚本219.57s，失联/恢复子测试129.36s，/tmp/router-ipv6-vm-lease-loss.log及制品/tmp/router-ipv6-vm-lease-loss。真实停止Kea后LAN PD地址自然消失、DHCPv6 DNS清空，RA默认路由/SLAAC与IPv4/DNS保留；重启Kea自动重新获取IA_NA/PD并恢复clientIPv6，随后禁用清理也通过。
- 扩展验收：新增强制IPv6传输的LAN DNS查询、router整机reboot后last-good双栈恢复、DHCPv6无PD及SLAAC-only模式（WAN可通、LAN无前缀/转发关闭、IPv4正常），再恢复PD后验证禁用。语法/diff通过，最新62388运行中（/tmp/router-ipv6-vm-reboot-modes.log，输出/tmp/router-ipv6-vm-reboot-modes），未提前判定通过。

- RA MTU 接入私有资源日志：写内核前保存 Original/Previous/Target，重试允许写入前后两种观察值，清理恢复原值并遵循当前物理链路上限；拒绝 all/default/lo 和外部 MTU 改动，忽略超过链路上限的通告，允许已消失 PPP 接口清理。仅修改 WAN IPv6 sysctl，保留 IPv4/物理 MTU。WAN6 回调 PATH 补 procps。
- MTU 定向 RED `/tmp/router-ipv6-mtu-red.log`，GREEN `/tmp/router-ipv6-mtu-green.log`；全量 Go 28478 终态退出0 `/tmp/router-mtu-full-go.log`。真实隔离内核 `/tmp/router-mtu-kernel.log` 通过0.497s：WAN 1500→1400→1500、物理及其他接口1500不变。
- 真实 bridge/路由报文 PMTU 测试61951终态退出0，2.442s，`/tmp/router-mtu-forwarding.log`：WAN IPv6 MTU1400时超大 UDP 收到 ICMPv6 Packet Too Big（携带1400及原始源/目标），小报文仍通过，已有转发/拒绝/禁用/外部接口隔离保持。尚不等同于端到端大 TCP/UDP 自动适配或整套新 MTU VM 通过。

- 更严格的 MTU 端到端 VM66967终态退出0：完整脚本295.40s，`/tmp/router-mtu-e2e-vm.log`，制品 `/tmp/router-mtu-e2e-vm`。真实 ISP RA AdvLinkMTU1400、WAN IPv6 sysctl1400但物理1500；超大 ICMPv6 收到 MTU1400 的 PTB，64KiB HTTP POST/回读完全一致，IPv4 1472字节payload仍通过；自然租约失效/恢复、整机重启、模式切换及最终关闭恢复WAN IPv6 MTU1500全部通过。大UDP自动分片重试仍未单独验收；上述证据不包含随后新写的 WiFiNetwork 草稿字段。
