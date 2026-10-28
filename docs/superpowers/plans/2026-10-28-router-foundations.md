# 家用路由实施计划：管理边界与保存反馈

> **For agentic workers:** Use superpowers:executing-plans for inline execution; preserve the full design acceptance matrix beyond this first plan.

**Goal:** 落实用户确认的独立管理器边界，并修复保存/应用反馈，避免页面把失败显示为成功。
**Architecture:** 保留 Go/JSON/SQLite 和内嵌原生 Web。运行应用前拒绝其他通用网络管理器；生成模式保持独立。LAN 改址不新增超时回滚。
**Tech Stack:** Go testing/httptest、NixOS 模块求值、Chromium 页面行为测试。
**Spec:** ../specs/2026-10-15-usable-home-router-design.md

## Global Constraints

- 用户 2026-10-28 三项决定优先；不新增确认窗口、断联看门狗或自动回退地址。
- 不修改宿主网络或现有预览数据；CommandRunner 隔离外部动作。
- 两份 app.js 保持同步；保留总览 JSON 移除。
- 本计划只交付基础切片，A01–A14 全部验收才构成完整路由器完成。

## Review Focus

冲突预检查必须发生在任何生成覆盖/运行写入之前，不能自动停用其他管理器；失败保存不能清除草稿；应用结果必须检查业务错误而非仅 HTTP 200。

## Task 1: 网络管理器互斥

- [x] 在 backend/internal/server/network_manager_test.go 通过真实 Apply handler 验证运行中/状态未知时拒绝，生成模式不检查，不执行修改命令。
- [x] 增加 network_manager.go 预检查并接入 server.go；覆盖 NetworkManager、systemd-networkd、dhcpcd、connman。
- [x] modules/default.nix 增加运行应用时的配置断言；tests/module-apply.nix 验证冲突及预览兼容。
- [x] RED/GREEN: `cd backend && GOCACHE=/tmp/nixos-router-go-cache go test ./internal/server -run 'TestApplyNetworkManager|TestApplyRuntimeGates'`。
- [x] Nix: `nix eval --impure --json --file tests/module-apply.nix`（若环境阻塞，记录原始原因，不宣称通过）。

## Task 2: 保存与应用反馈

- [x] tests/web-save-apply.cjs 用隔离后端与 Chromium 验证真实配置保存，重现 HTTP 200 含 error 误显示已提交、生成模式误导、保存后放弃草稿污染缓存。
- [x] backend/web/app.js 修复插件保存缓存/草稿完成状态及顶部应用结果；同步 web/app.js。
- [x] 单插件和批量插件统一为原子 /config PUT；成功才清除当前页 dirty，失败保留草稿，插件密钥占位符由后端合并。
- [x] 回归脚本验证成功、失败和生成模式；两份资源一致，JS 语法检查。

## Task 3: 验证与交接

- [x] `cd backend && GOCACHE=/tmp/nixos-router-go-cache go test -race ./...` 全套通过（server 188.142s）；最后新增空密钥类型修正后，最终代码 `go test ./...`（18.956s）及 `go test -race ./internal/server -run TestPutConfigPreserves -count=1`（3.414s）通过。
- [x] 检查 diff、资源同步、独立复核，记录未覆盖项。
- [x] 不提交、不合并、不部署未完成的真实网络接管；交付代码及测试结果。

## 后续完整目标（本计划不将其标为已实现）

配置版本/迁移/并发冲突；低权限 API + 受限执行器；异步应用和失败补偿；WAN/LAN/桥/DHCP/DNS/nft 实际生命周期；改址重连提示；真实状态；WiFi/访客/PPPoE/IPv6；高级插件；隔离三节点及目标硬件 A01–A14 验收。

## 执行记录

- 独立工作区 /tmp/nixos-router-home-router，分支 feat/20261028-home-router；原始暂存内容保留并复制。
- Go 基线通过。默认缓存只读，改用 /tmp 缓存。
- Nix daemon 访问被沙盒阻止；升级审批因连接中断未完成，命令未执行。未绕过审批。

- 调整依据：后端没有注册 /plugins/ 子路径；统一 /config 原子保存，取消逐插件 PUT 与 404 回退。对应范围维持一个权威配置入口。
- Nix 审批连接恢复后重新求值通过：defaults=true、optedIn=true、四项 exclusiveManagers=true。
- 7 项浏览器回归通过；两份 JS 同步且语法检查通过。
- 独立审阅指出缓存引用、插件密钥和 409 详情反馈缺口，均增加复现测试后修复；最终复核未发现新增重要问题。非敏感 URL 清空和新增空密钥类型也已纳入 API 回归。
- 单元测试覆盖管理器的 active/activating/deactivating/reloading/failed/空状态及查询错误；不自动停止管理器。进程生命周期互斥和真正网络接管仍属后续实施，不夸大当前 preflight 能力。

- 最终交付在独立工作区，未提交、合并或部署。原工作区暂存状态复核不变。完整家用路由目标仍未完成。
