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
- [x] RED：安全引用密码（空格、#、双引号、反斜杠）、拒绝控制字符/缺失账号、固定接口/MTU1492/重拨选项、DHCP/static出口保持原接口。
- [x] 实现纯渲染辅助函数；非PPPoE返回空字符串；当前 managed 校验仍拒绝PPPoE，直至Task2完整接线。
- [x] go test ./internal/managed、vet；本地提交。

## Task 2: 专属服务与运行链路

Files: manager.go/render.go、modules/managed.nix、status/API/Web能力、tests/module-managed.nix。
- [x] RED：所有权冲突拒绝、模式切换stop/start序列、私有文件权限、PPP出口NAT/DNAT、候选回调generation校验、无地址与观察错误的状态。
- [x] 接入渲染和固定出口，防火墙同时保护物理和PPP接口；MSS按实际出口MTU限制。启动专属pppd，守护断线重连，禁用时停止。凭据仅通过0600文件传递。Nix固定插件/钩子/设备权限。
- [x] 回调更新专属WAN resolver，不操作全局resolv.conf；标记generation，旧回调拒绝；读取运行态地址+服务状态，连接等待/认证失败不报联网成功。
- [x] 全量Go/race、模块eval、浏览器真实API及能力回归；具体字段和状态协议随代码定位后在ledger记录。

## Task 3: 真实PPP隔离验收与提交

Files: 新建 tests/managed-pppoe.py、对应Nix VM定义与flake/CI接线；更新testing/landing/status。
- [x] 模拟PPPoE上游、router、LAN client：正确凭据真实拨号/NAT/DNS/DNAT、错误密码、上游断开重连、模式切换、关闭清理、重启恢复。
- [x] WAN管理访问拒绝、LAN可用；凭据不在ps、普通用户不可读文件；PPP服务重启后真实客户端流量恢复。
- [x] 新PPP VM + 既有有线/WiFi/privsep/legacy VM回归；独立整变更审阅一次，Important按RED→GREEN修复。
- [x] 文档准确区分本地测试、CI、PR、硬件验收；不因缺少真实gw配置缩小通用功能目标。

## Rulings / execution ledger

- 使用现有完整设计的专属 pppd 方案，无新增产品范围，无需再次批准。工程选择：固定 nr-ppp0、MTU/MRU1492、LCP保活10秒/3次、重试间隔5秒；这些默认值仍须VM证据。
- 2026-11-09 基线0ca8e8b，分支feat/20261109-managed-pppoe。当前源码在validate中拒绝PPPoE，所有nft出口使用物理WAN；不能仅移除拒绝就宣称支持。
- 核对本地ppp2.5.2手册：ifname冲突会退出，dryrun会输出参数，因此生产预检不使用dump/dryrun泄露配置。官方参考 https://ppp.samba.org/pppd.html 。

- Task 1 本地验证完成：TestPPPoEOptionsAndEgress / TestPPPoERejectsUnsafeOptions 先因缺少实现RED，再通过完整 managed 包测试（2.633s，/tmp/router-pppoe-render-tests.log）；vet/diff --check退出0。实现只新增纯函数和测试，尚未接入生产运行入口，managed PPPoE拒绝仍保持。
- Task 1: Ruling: 生成nodefaultroute，由后续受generation保护的ip-up钩子添加默认路由；DNS也由专属钩子写私有resolver。后续必须核验pppd usepeerdns会写/etc/ppp/resolv.conf的副作用，通过服务文件系统隔离避免触碰宿主PPP配置。代价：Task2钩子和隔离未完成前此生成器不构成可拨号实现。
- Task 1: Ruling: 凭据1-255字节、有效UTF-8、拒绝控制字符；符合pppd常见凭据长度边界，错误不回显原值。未在真实pppd解析器/ISP验证，Task3须使用带特殊字符的测试凭据证明正确，而不是只依赖字符串断言。

- Task 2 本轮实现（未提交）：managed允许已验证格式的PPPoE候选；generation生成0600 pppoe.options并附ipparam；独立服务启停、物理WAN及PPP防火墙保护、PPP NAT/DNAT和MSS1452；nr-ppp0外部alias拒绝，固定名称不允许配置为其他受管接口。无PPP地址报告awaiting-pppoe-session，观察错误报告wan-status-unknown。
- Go回归 TestPPPoEApplyAndSwitchBack / TestPPPoERejectsForeignInterfaceBeforeMutation 已RED→GREEN；完整managed包3.010s（/tmp/router-pppoe-lifecycle-go.log），vet/diff --check通过。
- Nix模块先观察缺少nixos-router-pppoe服务断言RED（/tmp/router-pppoe-module-red.log，会话11138终态退出1）。随后实际写入专属服务和pre-up/up/down钩子，语法解析退出0。钩子校验第六参数generation，PPP专属接口；DNS写私有resolver；TemporaryFileSystem隔离/etc/ppp；pppd通过文件读取秘密，不进入ExecStart。
- Nix最终评估尚未执行：第一次组合命令自动审查超时未执行（cell471）；已拆分并完成沙箱内文件编辑/语法检查，单独重试评估仍因自动审查连接中断而未执行（cell474）。这些不是代码测试失败，也不是安全判定。该exec内后续全量Go命令也未开始，无存活测试句柄，不要按日志缺失推断测试通过。不要绕过审批；权限服务恢复/得到指导后再运行评估。
- Task2尚待：模块评估、回调真实执行验证、DNS/路由切换清理和认证失败状态、API/Web能力声明同步、全量后端/浏览器。Task3真实PPP VM和独立整变更审阅尚未开始。当前未提交，不声称已可部署。

- 2026-11-09 后续进展：审批服务恢复，通过原审批机制启动评估（未绕过）。模块评估会话29585已终态退出0，/tmp/router-pppoe-module-green.log全部assertions为true。全量Go会话50902退出0（server47.244s，/tmp/router-pppoe-runtime-go.log）。
- 新增tests/vm-managed-pppoe.nix及managed-pppoe.py、flake/CI入口：复用既有router/LAN客户端，新增合成PAP凭据的rp-pppoe上游；拨号/peerDNS/NAT/DNAT/错误密码/断线重连/DHCP切换/重启恢复与管理隔离均有真实断言。Nix/Python语法通过，但不能视为VM已通过。
- 新真实VM构建会话24110仍运行，日志/tmp/router-pppoe-vm.log。必须先查询这个句柄，不能因无输出重启。此构建快照早于本轮server能力声明/UI修改，只证明网络实现快照；最终版本还需适当复验。
- 测试发现PPPoE切回DHCP保留旧peerDNS；TestPPPoEApplyAndSwitchBack新增断言RED→GREEN，旧协议服务全部停止后切换generation并原子清空私有resolver；完整managed包3.757s通过（/tmp/router-pppoe-resolver-go.log）。
- TestManagedHealthReportsPPPoECapability已观察RED，随后server能力列表加入pppoe并移出unsupported；尚待GREEN。新增浏览器能力说明回归会话90529正在运行RED阶段，日志/tmp/router-pppoe-ui-red.log，需先查询；Web文案尚未修改。

- TestManagedHealthReportsPPPoECapability GREEN（会话46803退出0）；Web deployment说明按wanModes是否含pppoe显示，旧接口保守兼容。浏览器新用例先遇fixture误等WAN页checkbox超时，修正为wifi入口后才观察真实旧文案断言RED（/tmp/router-pppoe-ui-red2.log）；随后7/7能力浏览器GREEN（会话49987退出0，31.782s，/tmp/router-pppoe-ui-green.log）。此浏览器使用此前临时后端与受控health响应，不能代替新后端完整浏览器回归。
- 当前唯一存活测试句柄：VM会话24110，已完成包构建并启动router/upstream/client三个节点；尚未有拨号验收终态。其他会话29585/50902/24097/13856/90529/88309/46803/49987均终态。下一轮先poll24110及读/tmp/router-pppoe-vm.log，不重启；修复首个真实失败后再进行必要重跑。Task2/3未完成，尚未提交。

- 首轮VM会话24110终态退出1：实际PAP认证成功、PPP路由、LAN ping/NAT和peer DNS均已通过；随后测试从ip-address JSON读取ifalias导致KeyError，尚未验证后续场景（/tmp/router-pppoe-vm.log）。将所有权断言改为直接读取/sys/class/net/nr-ppp0/ifalias，未删除断言，也未改生产所有权逻辑。
- 第二轮VM会话16940正在运行（/tmp/router-pppoe-vm-second.log），已构建完并启动节点。该快照不含后续服务状态列表更新及测试增强，必须先查此句柄，不因等待重启。
- TestManagedUnitsIncludeDedicatedPPPoE RED→GREEN：managed服务状态加入独立pppoe服务；状态来源仍为真实systemctl，失败/未知不改写为active，不将service active视为联网。全量Go会话38102退出0（server54.213s，/tmp/router-pppoe-status-go.log），managed race会话18939退出0（8.265s，/tmp/router-pppoe-race.log）。
- 测试增强已写入待下一轮VM：含双引号/反斜杠/#/空格的合成密码，DHCP切换后重放旧generation up-hook不能修改resolver/路由；核对schema发现DNAT fixture应使用destIp而非destIP，已修正（第二轮快照仍有旧fixture，预期此处需要重跑）。
- 最新临时后端/tmp/router-pppoe-web-routerd已构建。完整浏览器会话79279运行中，日志/tmp/router-pppoe-browser-all.log；vet/diff检查会话14985待查询。未提交，真实完整PPP验收、旧VM回归、独立审阅仍未完成。

- 第二轮VM16940终态退出1：sysfs所有权断言通过，第一组拨号/peerDNS/NAT/凭据文件权限/ps和journal不泄密/WAN管理拒绝全部通过；第二组DNAT因旧fixture destIP被严格API400拒绝，符合预先核对发现。没有为迁就fixture放松API。
- 第三轮VM已启动，/tmp/router-pppoe-vm-third.log，包含修正destIp、特殊字符密码、旧generation回调以及最新服务状态接口。当前代码未继续修改，等待该轮结果。完整浏览器79279仍在运行；vet14985已终态退出0。
- 第三轮VM句柄86086，完整浏览器句柄79279，目前均经轮询确认运行；无其他存活测试句柄。继续时先查询这两个句柄，不能按日志静默重启。此次工作未提交，后续仍需真实全部PPP场景、既有四套VM回归和独立审阅。

- 完整浏览器79279已终态退出0，44/44，190.224s（/tmp/router-pppoe-browser-all.log）。
- 既有四套VM回归会话29631已启动并确认运行，日志/tmp/router-pppoe-regression-vms.log，使用第三轮不可变源码/nix/store/kvy45g84pvb38pm0i0803666j0gz4jl4-ckq775mkmmrpk7v2jd5g25r3ji5jf3df-source串行--max-jobs1，不是用户原工作区。
- 第三轮86086终态退出1：产品与fixture构建完成，但测试driver类型检查发现re.search可能None而直接.group。未运行第三轮流量，不关闭typecheck；改为先assert hook_match is not None再.group。
- 第四轮42035已启动，/tmp/router-pppoe-vm-fourth.log。/tmp/router-pppoe-test-driver.nix使用上述不可变产品+fixture，仅testScript指向当前修正的managed-pppoe.py；这是测试脚本修复，不改变被测产品，避免因仅测试脚本变更重复编译产品。后续提交/CI仍使用仓库标准flake check。
- 当前需先poll42035和29631；其余先前测试句柄均终态。文档改为当前实现/验收状态，未将部分通过当作PPP完整通过。尚未提交/独立审阅。

- 第四轮42035终态退出1，发现真实产品问题：认证失败创建的nr-ppp0尚未到ip-pre-up，因此没有所有权alias，用户修正密码后preflight误拒绝为foreign PPP。前三组已通过：特殊字符密码/peerDNS/NAT/凭据与管理边界13.37s，DNAT增删19.08s，上游断开及自动重连9.27s。错误密码的无默认路由和LAN管理可用也通过，但改回正确密码被拒绝。
- 修复：使用pppd同步net-init-script，在LCP/认证前标记接口；此钩子的ipparam是第4参数，上下线钩子仍为第6参数。保留generation校验和foreign接口拒绝，不以放宽所有权绕过问题。核对本地ppp2.5.2手册的net-init脚本生命周期及参数。tests/module-managed.nix增加net-init-script断言；语法/diff检查通过。必须等真实VM修复后GREEN，不能只按源码判断。
- 第五轮标准flake VM会话19604已启动，日志/tmp/router-pppoe-vm-fifth.log；产品Nix模块已变，不再复用旧产品fixture。既有四套回归29631继续运行；其中managed-router已完整通过317.57s（该快照早于仅PPP net-init修复，不能宣称全部最终源码已通过），正在privsep。

- 第五轮标准PPP VM19604已终态退出0（/tmp/router-pppoe-vm-fifth.log）：完整已有场景通过，包含错误密码修正重拨、DHCP切换、旧generation回调隔离及重启恢复。实际生成net-init钩子已核对为第4参数，无多余引号。最终模块评估23105退出0，全部断言true（/tmp/router-pppoe-module-final.log）。
- 既有四套回归29631退出0：managed-router317.57s、privsep97.12s、managed-wifi258.02s、legacy-router72.51s；使用此前记录的第三轮不可变快照，早于Nix net-init修复，不声称最终所有源码同轮通过。
- 按executing-plans完成一次独立整变更审阅（pppoe_final_review）：P2产品问题为MSS硬编码1452未适配较低协商MTU；P2证据缺口为缺少本地pppd崩溃/专属服务重启。接受两项，尚未修复MSS，不能关闭Task2/3或宣称可部署。
- 新增真实VM本地pppd SIGKILL、MainPID变化、LAN管理可用、重新拨号流量及显式服务重启验证。会话42084正在运行，日志/tmp/router-pppoe-crash-vm.log；/tmp/router-pppoe-test-driver.nix使用第五轮不可变产品快照/nix/store/r8c0q84nhly8sqyysczgd8qq88s40m8g-m8pcn1dfazir5a38g4j7sn5x9s6j3pfk-source，仅测试脚本取当前版本，未改被测产品。继续时先poll42084，不因静默重启。
- landing文档已移除PPPoE不支持的过时声明，说明独立服务、私有凭据及等待会话不代表联网。下一步：完成较低MRU真实回归RED、按协商MTU修复双向MSS并GREEN，必要全量回归与文档更新，再提交；不重复委派审阅。没有操作gw。

- pppd崩溃/专属服务重启增强VM42084退出0，完整脚本161.07s，局部崩溃与重启恢复8.88s（/tmp/router-pppoe-crash-vm.log）；没有通过重写网络配置救活测试。
- 新增低MRU真实测试：模拟ISP只降低MRU为1400，要求router实际MTU1400；读取TCP连接两端TCP_MAXSEG，要求<=1360，并校验双向2MiB数据完整性。第一轮17687退出1，停在MTU前提，未进入产品MSS RED：rp-pppoe命令行覆盖-O文件MRU。修正为文档支持的-q包装器，在pppd参数最后设置mru1400；不修改被测路由器。
- 第二轮低MRU定向VM42527运行中，/tmp/router-pppoe-mss-red2.log，使用第五轮不可变产品和/tmp/router-pppoe-mss-only.py；产品MSS尚未修改。下一步先确认实际MTU与MSS RED，然后在ip-up中根据实际MTU原子更新既有ppp_mss链，再装默认路由；保留generation校验、双向仅降低MSS。不要用仅出口rt mtu规则代替双向限制，因为入站SYN-ACK的查路由出口是LAN。

- 低MRU第二轮42527退出0，但TCP直接终止于PPP对端，主机自身MTU会限制MSS，不能证明路由器规则正确。修正测试拓扑：ISP后方增加独立1500-MTU网络命名空间10.99.0.2，路由器仍只通过真实API配置。第三轮87229因systemd环境内python3相对路径不可用失败，改用绝对路径；此为fixture失败。
- 第四轮61233得到真实产品RED：接口MTU1400，双向2MiB数据校验通过，但连接两端TCP_MAXSEG均1440，超过1360（/tmp/router-pppoe-mss-red4.log）。保留该断言，不以数据因ICMP纠正后可通作为MSS正确证明。
- MSS修复已写入modules/managed.nix：同步ip-pre-up钩子读取实际MTU，校验范围128..1492，使用一次nft事务flush/add既有ppp_mss链，双向仅降低到MTU-40；net-init的认证前所有权标记和generation检查保留。Ruling: 使用同步pre-up而非原拟异步up——保证IPCP开放流量前规则已更新，避免短暂旧上限窗口。
- 完整最新产品PPP VM23461运行中（/tmp/router-pppoe-mss-green.log）；模块评估43126运行中（/tmp/router-pppoe-module-mss.log）。需先查询这两个句柄。其他低MRU/崩溃测试均终态；不能将RED的旧产品快照与修复后完整验收混淆。尚未提交，目标仍含IPv6/访客/高级模块等，不能标记整体完成。

- 最新标准flake PPP VM23461退出0，完整脚本105.29s（/tmp/router-pppoe-mss-green.log）。低MRU实际MTU1400、双端MSS1348，双向2MiB内容校验通过；全部此前场景含本地SIGKILL/服务重启均通过。模块43126退出0全部断言true。独立审阅两项P2均已处理并有运行证据。
- 最终沙箱Go37364失败于沙箱将/tmp映射为65534及禁止Unix socket；这不是通过。保持权限校验不变，经审批在正常环境重跑69131（/tmp/router-pppoe-final-go-host.log）待终态。vet/diff/双份Web一致性44438退出0。浏览器仍是同一Go/UI逻辑的44/44，后续仅修改Nix钩子与测试文档。完成提交仍须全量Go终态通过。

- 正常环境最终全量Go69131已退出0（/tmp/router-pppoe-final-go-host.log）；产品权限逻辑未为沙箱测试放松。Task1/2/3实现与本地验收已完成，准备提交。PPP完成不等于A01–A14整体完成；保留原工作区用户修改，不接触实体gw，不合并。
