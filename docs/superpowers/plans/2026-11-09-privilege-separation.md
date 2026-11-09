# managed 权限隔离实施计划

> **For agentic workers:** 使用 superpowers:executing-plans 逐项实施。

**Goal:** 按完整设计第 5 节将网络管理从 root HTTP API 移入独立受限执行器。

**Architecture:** 使用 Linux Unix socket 和 SO_PEERCRED 双向身份校验。执行器接收配置级操作，自行校验并调用 managed.Manager；不接受命令、任意文件路径或接口授权变更。API 管理账户、草稿和作业，执行器独占运行配置、last-good、候选和网络事务。

**Tech Stack:** Go、Linux Unix socket、现有 managed.Manager、NixOS systemd。

**Spec:** [完整设计](../specs/2026-10-15-usable-home-router-design.md)，已获授权自行实施；具体 IPC 方案为本次工程选择。

## 约束

不操作实体 gw 或开发宿主网络；不添加 LAN 改址超时回滚；保留生成模式；不暴露 shell/argv 执行 API。此分支基于 PR #16，权限隔离完整验收前不宣称 root API 问题已解决。

## Review Focus

- 伪造本地调用者/执行器：使用内核 peer credentials，不信任 JSON 或 HTTP 头中的身份。
- socket 路径劫持：父目录必须由服务账户拥有且不可被其他用户写入；不覆盖已有路径。
- API 被攻陷：不能写执行器的候选、last-good、服务配置，不能提交额外命令和授权接口。
- API/执行器单独重启或 RPC 断连：不把未知结果报告成功，不重复提交；恢复使用执行器持久化结果。
- 旧状态目录升级：迁移前校验归属及文件类型，保留账户/草稿与有效 last-good，不能将 API 可写文件作为 root 命令配置读取。

## 1. 认证传输

文件：`backend/internal/privsep/unix_linux.go`、`unix_linux_test.go`。

接口：`Listen(path string, allowedUID uint32) (net.Listener, error)`；`DialContext(ctx context.Context, path string, serverUID uint32) (net.Conn, error)`。

- [x] 先写真实 Unix socket 测试：允许账户双向通信；错误客户端 UID 不被 Accept 返回；客户端拒绝错误服务 UID；拒绝危险目录/已有文件且不破坏内容。
- [x] 运行测试确认缺失实现导致失败，实现 SO_PEERCRED 校验及受保护路径绑定。
- [x] 定向 race 和全量 Go 测试通过；独立提交这一通信边界，不宣称已完成服务迁移。

socket 初始权限 0600；不同 UID 的访问权限由后续特权 daemon 在受保护目录中显式授予，不能扩大到其他用户。Listen 的父目录只允许当前 EUID 拥有且无 group/world 写权限；父路径不得包含 symlink；祖先必须由 root 或服务账户拥有且不可被其他用户写入，root 拥有的 sticky 目录（如 /tmp）例外。客户端也校验父路径并验证服务 UID，不依赖可伪造的 socket 文件内容。

## 2. 配置级 RPC 和独立执行器

文件：新增 `backend/internal/privsep/protocol_linux.go`、`client_linux.go`、`journal_linux.go`、`state_linux.go`、相应测试和 `backend/cmd/router-executor/main.go`。

- [x] 定义版本化、大小有界且拒绝未知字段的 apply/status 请求；传入配置而不是路径/命令。
- [x] 执行器独占 managed.Manager；apply 保持有界串行任务和持久化结果，连接断开不取消或重复网络事务。API 重启可按任务 ID 查询；ID 内容绑定避免重复 ID 换配置。
- [x] 真 socket 集成验证未知动作/超大消息/非法配置/未授权接口被拒绝，合法调用保留结果与恢复错误；模拟仅限系统 runner。
- [x] 独立执行器接受受信启动参数、私有状态目录和 bootstrap；恢复前拒绝不可信状态子树。
- [ ] 在 NixOS 接线中实际分离 root 状态目录与低权限 API 目录（Task 3），并完成不同 UID 与服务重启验证（Task 4）。

## 3. API、模块与升级

文件：`backend/cmd/routerd/main.go`、`backend/internal/server/managed.go`、`modules/default.nix`、`modules/managed.nix`、模块与启动测试。

- [ ] 用 RPC client 替换 API 内的 manager 初始化；API 无 root、CAP_NET_ADMIN 或任意 systemctl 权限。
- [ ] 状态、无线能力、租约等必要只读数据经受限查询/快照提供；失败明确 unavailable。
- [ ] 定义 systemd 服务用户、私有运行目录、socket 授权、启动顺序；引导密钥通过受控方式供账户初始化。
- [ ] 旧布局迁移测试证明账户/草稿/last-good 保留，symlink 和不可信运行文件拒绝；损坏草稿恢复契约继续有效。

## 4. 完整验证与交付

- [ ] 全量 Go/race、浏览器、Nix 模块；新 VM 断言 API UID 非 0、非法客户端不能执行、API 无法篡改 root 状态。
- [ ] 原 wired/wifi VM 全量通过，追加 API/执行器独立崩溃及连接中断场景，不预设被测路由行为。
- [ ] 独立代码审查、更新接入指南/完成度文档，提交 PR。未经用户同意不合并或实施 gw。

## Task 1 验证记录

- 缺失 Listen/DialContext 时测试编译失败；补充实现后真实 socket 与定向 race 通过。
- 独立审查要求保护祖先目录、避免 Close 删除替换路径；两条回归均先观察失败再修复。
- `go test -race ./internal/privsep`、`go test ./...` 通过；后者既有包可命中有效缓存。
- 测试只创建 /tmp 下私有目录和 Unix socket；未执行网络配置命令。
- CodeGraph 经用户授权初始化；索引和生成的 Cursor 规则留在本地，不纳入功能提交。
- 尚未接入 API/daemon/Nix 服务。不同真实 UID 通信、root 运行目录迁移、进程崩溃以及网络行为仍按 Task 2–4 验收；不能据此宣称权限隔离完成。

独立复审已确认 Task 1 无剩余阻塞问题；复审为源码检查，运行证据来自上述实际测试。

## Task 2 验证记录

- 新增配置级 `/v1/jobs` 与 `/v1/applied`，单次 JSON 请求上限 1 MiB；拒绝未知字段、尾随 JSON、非法 ID/配置、任意命令路径。Serve 只接受已认证 Unix listener。
- 执行器 SQLite 持久化 ID、配置摘要与结果；相同 ID/配置返回原任务，换配置冲突；仅一个活动任务，不排无限队列。持久化接受失败不执行；结果落盘失败不报告成功并拒绝继续应用。
- 真实 socket + managed.Manager 测试通过；仅系统命令使用 runner。覆盖未授权接口、应答丢失后查询/重试、并发拒绝、应用失败恢复成功/失败及日志重开。running 行重开标 interrupted，不自动重放。
- 独立审查发现顶层目录验证不足：恢复前必须检查 managed 子树。五种不可信目录/配置/硬链接回归先失败；增加 CheckState 后通过。保留引擎的受限 generation 指针，并允许私有父目录内 root 组的 hostapd 控制 socket。
- 故障注入初次误匹配 start，实际命令为 restart；修正测试夹具后恢复成功/失败两种结果均通过，不改动引擎来迎合测试。
- `go test -race ./internal/privsep ./cmd/router-executor`、`go test ./...`、定向 `go vet` 通过。`go build -buildvcs=false -o /tmp/router-executor-check ./cmd/router-executor` 构建成功；该 worktree 的默认 VCS stamping 曾失败，未将该环境错误当成代码通过。
- 尚未启动特权 executor、未操作宿主网络。真实不同 UID、损坏状态阻止 systemd 服务启动、API/执行器进程强杀和恢复仍须 Task 3/4 的 VM 证据；本轮 journal 状态夹具不代替进程崩溃测试。
- PR #16（c83ce4f）的 Go/Browser/VM CI 已全部通过，VM run 为 42587212570；此证据不覆盖本权限隔离分支的新代码。
