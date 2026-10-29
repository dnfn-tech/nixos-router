# gw Readiness Implementation Plan

**Goal:** 交付可由用户验收的 gw IPv4 基本网关硬件测试前置版本与真实隔离证据。
**Architecture:** 独立 managed 包负责网络生命周期，现有 server 管理 API/鉴权/草稿，NixOS 专属协议服务负责 DHCP/DNS/AP。
**Tech Stack:** Go、原生Web、NixOS、QEMU、dnsmasq、udhcpc、nftables、hostapd。
**Spec:** ../specs/2026-10-28-gw-readiness-design.md

## Global Constraints

不访问实体gw；不修改开发宿主网络；不新增LAN改址自动回滚；管理器互斥；现有预览与UI保持兼容；高级未实现能力拒绝启用而非伪报成功。

## Review Focus

- 重启不能应用未应用草稿；成功配置保存失败不能报告成功。
- WAN和非受管接口不能被错误桥接/清空，DHCP/NAT旧资源必须清理。
- 失败发生在接口、服务或规则激活中途必须恢复并记录独立恢复失败。
- 输入换行、接口/域名/密钥与Origin绕过不能变成命令或配置注入。
- 并发读写/应用/恢复不得破坏配置、密钥或作业状态。

## Task 1: managed 网络引擎（独立包）
Files: backend/internal/managed/{manager.go,render.go,manager_test.go,render_test.go}
Interface: Options{StateDir string, Interfaces []string, WiFiInterface string, ManagementPort int, CountryCode string, Runner Runner}; Runner.Run(context.Context,string,...string)(string,error); New(Options)(*Manager,error); Apply(context.Context,config.Config)(Result,error); Restore(context.Context,config.Config)error; Result{Stage,RollbackStatus string; Warnings []string}.
- [x] 写失败测试：接口归属/冲突、完整dnsmasq/nft/hostapd生成、敏感权限、非法输入、禁用撤销、失败恢复、last-good启动、拒绝未实现能力。
- [x] 实现并运行go test ./internal/managed；所有宿主命令在测试中隔离。
- [x] 记录真实VM需要的专属单元与文件契约。

## Task 2: API并发与管理面（与Task1文件独立）
Files: backend/internal/server/server.go, backend/internal/server/*security*_test.go, backend/internal/server/*concurrent*_test.go
- [x] Origin精确匹配失败测试，修正子串匹配；并发配置/客户端/恢复/status/登录race测试并修复共享状态。
- [x] 命令context超时、Apply busy拒绝，保留旧预览契约。
- [x] go test ./internal/server 与针对新用例go test -race通过。

## Task 3: 集成与NixOS部署
Files: backend/cmd/routerd/main.go, backend/internal/server/managed.go, modules/managed.nix, modules/default.nix, tests/module-managed.nix
- [x] 新运行模式Options和CLI、API Apply结果、启动恢复与capabilities的失败测试。
- [x] Nix模块明确受管接口/引导配置/协议单元，禁止与通用管理器及其他DHCP/防火墙所有者混用。
- [x] 专属udhcpc脚本、dnsmasq/hostapd单元及状态读取接线；Nix评估与构建通过。

## Task 4: API驱动真实网络验收
Files: tests/vm-managed-router.nix, tests/managed-router.py, flake.nix
- [x] 建立上游/网关/客户端隔离拓扑；确认旧实现无法通过动态地址池/DNS/端口转发/MAC测试。
- [x] 验证全部Spec场景，补齐引擎实际集成缺口；不降低断言以迁就实现。
- [x] 无KVM尝试TCG；保留完整日志与失败证据。

## Task 5: 无线软件链路、回归及交付
Files: tests/vm-managed-wifi.nix, docs/gw-readiness.md, docs/gw-hardware-acceptance.md, frontend capability messages
- [x] 无线模拟关联/WPA/DHCP与禁用/重启，真实驱动限制留给硬件验证。
- [x] Go/race、23项浏览器、VM、Nix全量必要回归；独立代码审查并修复重要问题。
- [x] 生成制品与版本摘要、门槛证据、迁移和人工恢复步骤；只在所有必需证据通过时宣告硬件验收前提具备。

Execution: Task1与Task2按dispatching-parallel-agents并行，文件所有权分离；主agent负责Task3-5。每项先失败后修复。用户已明确授权自行实施，不重复请求设计批准。没有提交/合并/实体部署步骤。


## 早期验证记录（2026-10-28，历史阶段）

- Task1/2 单元、并发 race 已通过；假 runner 只证明逻辑和命令边界，不能代替真实网络证据。
- Task3 managed模块8类冲突求值通过；异步job可读性、202先刷新、运行密钥种子、坏草稿恢复测试通过。Nix实际构建及整机链路进行中。
- 首轮TCG四节点已通过真实DHCP/DNS/NAT，重复Apply因自有网桥归属误判失败；保留 `/tmp/router-managed-build.log`，当前不能宣布硬件前提完成。
- UI既有23项及新增managed能力2项通过；这些是界面证据，不是无线关联证据。


## 第7轮候选关闭记录

统一候选第7轮：有线TCG220.86秒、无线hwsim195.60秒均通过；后端全量race通过；浏览器27/27通过；新旧模块评估均通过。新增激活中途强杀与真实SSH多端口隔离也在此轮验证。源码集合与被测试Nix快照逐文件零差异。

独立审查的P1新增桥成员崩溃残留、P1草稿SSH端口与实际监听不同、P2generation无限累积已修复并复核。其余早期集成问题（alias创建未落地、坏草稿启动、协议服务就绪、动态DNS权限）均有回归或真实VM证据。

原generate-only实现没有managed引擎及动态网络执行路径，不能满足新VM的引导与动态断言；未为了重放旧基线而覆盖原工作区。

本地generate-only预览已更新并保留配置、旧容器和备份。最终软件前置门槛完成；实体gw操作未开始，待用户验收后决定。完整结果与制品见 ../../gw-readiness.md。


## 提交前复审关闭记录

用户追加授权：检查其他问题，修复验证后提交代码并创建PR；不合并、不操作实体gw。

新增确认并修复：SSH ListenAddress显式端口遗漏、总览轮询隐藏/忙碌后停止、健康接口失败解除宿主管理只读。模块及3条浏览器回归先失败后通过。最终浏览器30/30（62.12秒）、全量Go/race与vet、新旧模块断言通过；统一最终候选有线VM172.30秒、无线VM157.55秒通过，运行源码与被测Nix快照零差异。新增SSH fixture的IPv6地址格式错误已按真实sshd诊断修正，失败构建不计作通过证据。

CI接入两个managed VM与模块断言；本地预览已更新、配置不变。最终制品见验收报告，原工作区修改保持不变。
