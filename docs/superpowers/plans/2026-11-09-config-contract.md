# 配置版本与并发契约实施计划

> **For agentic workers:** 使用 superpowers:executing-plans，自行逐项实施，保留完整家用路由目标。

**Goal:** 完整设计第 3 节与 A03：配置格式可识别、升级不丢字段，过期保存不覆盖其他会话。

**Architecture:** Config 内置 schemaVersion 与不透明 revision。旧配置通过独立草稿加载入口备份并迁移，运行快照只读加载。API 在现有 cfgMu 临界区内比较修订、合并秘密、保存新修订；Web 持有编辑开始时的修订并处理 409。

**Tech Stack:** 现有 Go/JSON/SQLite、原生 JavaScript；不增加前端框架。

**Spec:** `../specs/2026-10-15-usable-home-router-design.md` 第 3、4 节及 A02/A03/A10。

## Global Constraints

不操作宿主网络或实体 gw；不添加 LAN 改址超时回滚；两套 Web 资源保持一致。此工作从权限隔离提交 1e56377 开始，推送凭据阻塞不阻塞本地开发。

## Review Focus

- 未知字段、重复字段、未来版本：拒绝且不覆盖原文件。
- 旧配置迁移失败或已有不同备份：保留原配置及备份，不自动覆盖。
- 两个会话同时保存：仅一个匹配修订的请求成功；失败保留输入。
- 插件、设备、恢复等旁路修改：共享同一配置修订，不绕过并发控制。
- 恢复旧备份或重启：不能重用旧修订而让过期请求重新有效；作业捕获原修订。

## 1. 严格配置格式

文件：`backend/internal/config/document.go`、`document_test.go`、`types.go`。
接口：`Config.SchemaVersion int`、`Config.Revision string`；`CurrentSchemaVersion = 1`；自定义 `Config.UnmarshalJSON`，未知字段与重复 JSON 键拒绝，插件 config 的开放键保留；schemaVersion 0 为显式旧格式、1 为当前、其他拒绝。

- [x] 写回归并观察失败：未知嵌套字段、重复键、未来版本不被静默接受；旧格式及开放插件对象仍可读，密钥不出现在错误中。
- [x] 实现严格解码；只读 LoadFromFile 不改文件，Validate 拒绝非法版本/修订。
- [x] `go test ./internal/config` 与全量 Go 通过后提交独立格式基础。

## 2. 一次性草稿迁移

文件：`config/document.go`、`loader.go`、`cmd/routerd/main.go`、`remote.go`及测试。
接口：`LoadDraft(path string) (Config,error)` 与 `WithNewRevision(Config) (Config,error)`。revision 为 crypto/rand 生成的 16 字节十六进制字符串。迁移备份保存原始字节、0600、fsync；现有备份不同内容则拒绝。只有草稿加载入口迁移，last-good 不修改。原子写用唯一临时文件并 fsync 文件/父目录。

- [x] 测试旧字节/秘密备份、重启不重复迁移、坏配置无修改、备份冲突拒绝、只读快照不迁移。
- [x] 实现迁移和启动接线；保留坏草稿回退但不静默覆盖。
- [x] config/routerd 全量测试后提交。

## 3. API 与 Web 并发保存闭环

文件：新增 `server/config_revision.go` 及测试，修改 `server.go`、`managed.go`、两套 `app.js`、浏览器测试。
接口：GET config 返回 revision/ETag；所有配置写入在 cfgMu 内匹配 If-Match 并生成新 revision；过期返回 409/config_conflict，缺失返回 428/config_revision_required。恢复使用当前编辑基线，不复用备份 revision；apply 记录 configRevision。兼容读取旧 JSON，但新的写请求必须先 GET 取得修订；文档同步说明。

- [x] API 回归：两个并发写仅一个成功；插件/设备/恢复均受保护；失败不写磁盘、不丢秘密；重启和备份恢复不产生 ABA。
- [x] Web API 包装层传递编辑基线；成功仅更新已保存修订，冲突显示重新加载提示并保留输入，不自动重试覆盖。
- [x] 两浏览器会话真实 API 验证冲突/重新加载后保存；apply 作业修订与提交配置一致。
- [x] 同步现有 API/浏览器/VM 调用者取得修订，执行全量 Go、定向 race、浏览器及 VM；独立整分支审阅后提交。

## Ledger

Ruling: 按用户已批准的完整设计直接实施，继续使用现有隔离 worktree并建立独立分支；不重新请求已授权的工程选择。此计划不宣称 PPPoE、IPv6 或插件能力已完成。


### 实施记录

- 严格解码与迁移回归均先失败再通过；草稿启动路径迁移，运行快照不改写。未知字段/重复键/未来版本拒绝，备份冲突和 symlink 不覆盖原文件。
- API 并发回归：同修订两个写仅一个 200、另一 409；四类写入口缺条件 428。恢复不复用旧 revision；坏草稿回退获得独立修复修订且不改文件。
- Web 使用编辑开始时整份基线，不在保存时取新配置覆盖同字段。两真实浏览器会话验证冲突、保留输入、显式重新加载后成功；浏览器初轮 35/35、全量 Go、定向配置/并发 race 通过。
- 独立审查发现外层 envelope 绕过严格检查、大小写别名覆盖和开放插件大整数精度丢失。新增回归全部观察失败后修复：完整 envelope 严格解码、按精确 JSON tag 校验强类型字段、开放 map 保留键大小写，实际 decoder 使用 UseNumber。
- Ruling: 旧 JSON/备份读取兼容保留；无条件写 API 以 428 明确拒绝，不能为了兼容继续允许丢失更新。代价是第三方写脚本必须增加 GET/If-Match，已在接入指南注明。
- Ruling: 既有插件写处理器此前未注册子路径；补齐路由并受相同条件写保护，仍写入唯一 Config.Plugins 存储。
- 尚未宣称全部验收完成：审查修复后的最终全量/VM 和文档收尾仍在进行；开放插件数值通过 Go 文档迁移保真不等于浏览器 JavaScript 任意精度编辑保真。

### 续验记录

- 最终浏览器套件 35/35 通过（`/tmp/router-config-contract-browser-final.log`，退出 0），覆盖最终应用代码。全量 Go/vet 日志 `/tmp/router-config-contract-go-final.log` 已确认全部包通过。
- 新增不可变修订用例 `TestApplyKeepsCapturedRevisionDuringNewSave` 的 race 验证通过（1.695s）：应用期间另存草稿不改变候选、202 响应和终态作业的修订。
- VM 首轮仍在原有进程中执行；该轮快照早于严格 JSON 审查修复和有线 VM 修订断言，需要后续最终快照验证，不重复启动同一轮。
- Ruling: 格式、迁移及 API 是同一写契约的依赖链，本次统一提交，避免发布只能部分读写的新格式；原计划分步提交调整为完整闭环提交。

- 首轮四套 VM 均通过，构建进程退出 0：WiFi 163.21s、有线 346.14s、旧模式 72.78s、权限隔离 50.61s（`/tmp/router-config-contract-vm.log`）。该结果仅对应首轮代码快照。
- 已启动最终快照四套 VM，日志 `/tmp/router-config-contract-vm-final.log`；工具会话 65402，后续先查询该句柄，不因日志静默重复启动。

### 最终结果

最终四 VM 构建退出 0（`/tmp/router-config-contract-vm-final.log`）：WiFi 152.98s、有线 205.92s、旧模式 37.26s、权限隔离 30.92s。测试代码包含 apply 响应和终态作业 configRevision 断言。该快照为 Git 暂存区配置契约代码，不包含随后未暂存的类型化字段错误工作。

最终制品：privsep `wghblqkw5w8hcq76x49zw23ppvc75bc8`，managed-router `5394dbnw8yiggdzkzipa20cpr1ry3ki4`，managed-wifi `di3xhgxcpdh42lm01hbj6j2ac1d1j474`，legacy `kc9i2ybzsc1sajyjv14z16p8ndc68vpx`（均为 /nix/store 对应 vm-test-run 输出）。Go/vet、定向 race、35 项浏览器与四 VM 构成该子任务证据；完整目标及推送权限阻塞保持单独追踪。
