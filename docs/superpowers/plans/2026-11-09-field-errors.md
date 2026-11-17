# 配置字段错误与 API 失败响应实施计划

> **For agentic workers:** 使用 superpowers:executing-plans 原位实施；沿用完整设计已批准的自动推进方式。

**Goal:** 用户保存错误配置时获得稳定的错误代码、字段定位及保留的输入；所有 API HTTP 失败遵守统一响应结构。

**Architecture:** 配置校验产生带 JSON 字段路径的类型化错误，HTTP 层统一输出 code/message/fieldErrors。Web 保留结构化错误，并将字段路径映射到当前表单。已有错误字段只作为兼容别名；不从英文错误字符串猜测字段路径。

**Tech Stack:** Go 标准库、原生 JavaScript、现有 Playwright 测试。

**Spec:** `../specs/2026-10-15-usable-home-router-design.md` 第 3 节及 A02/A03；该计划只实现其中错误反馈部分，其余完整目标继续追踪。

## Global Constraints

- 不接管开发宿主网络，不操作实体 gw；不增加 LAN 改址倒计时或超时回滚。
- 两套 Web 资源同步；保留现有端点与 HTTP 状态语义、If-Match 和失败不落盘规则。
- 错误信息不得包含配置输入值、秘密、文件内容或内部路径；字段路径只能来自已知 schema。
- 409/428 无字段错误时不清空输入、不自动重试保存。

## Review Focus

1. 无效数组元素应定位到具体下标，不能把所有行标红；无法展示的字段显示可理解的页级提示。
2. 校验错误中即使填入密码形式的任意字符串也不能被回显；消息使用固定安全文案。
3. 修改失败字段后解除该字段错误，其他失败字段保持，第二次保存成功清除旧错误。
4. 认证、CSRF、未知 API 路径、错误方法和请求过大同样有 JSON 错误结构；静态文件行为不变。
5. Web 收到旧服务的 text/error/errors 响应仍能显示提示；structured fieldErrors 不得被当作 HTML。

## Task 1: 配置类型化校验

**Files:** 新建 `backend/internal/config/validation.go`、`validation_test.go`；修改 `types.go` 与 `document.go`。
**Interfaces:** `ValidationError { Path string; Code string; Message string }` 实现 `Error() string`；`Config.Validate() error` 签名保持。路径采用 `wifi.aps.0.psk`、`lan.staticLeases.1.ip` 的点分 JSON 名称，不包含用户自定义键。

- [x] 新增表驱动 `TestValidationErrorPaths`：空 hostname、无效静态 WAN CIDR、第二条静态租约 IP、第二个 AP 密钥、转发端口、DDNS 缺凭据；断言 errors.As、精确路径与固定 code，错误不含输入值。
- [x] 运行 `go -C backend test ./internal/config -run TestValidationErrorPaths`，观察 RED。
- [x] 将每个 Validate 分支改为类型化错误，保留现有有效/无效判定；数组使用实际下标，跨字段约束选定必须修改的字段。JSON 格式错误无可靠字段时保留全局诊断，未知键不回显键名。
- [x] 运行 config 全包测试，确认旧校验覆盖仍通过；提交该类型化校验单元。

## Task 2: 统一 API 错误响应

**Files:** 新建 `backend/internal/server/api_errors.go`、`api_errors_test.go`；修改 `server.go`、`config_revision.go` 及独立 HTTP handler 文件。
**Interfaces:** `APIError { Code string; Message string; FieldErrors map[string]string }`；`writeAPIError(w http.ResponseWriter, status int, code, message string, fields map[string]string)`。nil fields 输出 `{}`，不输出 null。

- [x] `TestAPIErrorContract` 覆盖错误 JSON、类型化配置错误、存储失败、过期修订、缺修订、登录失败、CSRF、方法错误、未知 /api 路径、请求过大；断言 HTTP 状态、JSON content-type、非空 code/message、对象 fieldErrors。
- [x] 配置 PUT/插件/客户端/恢复四个写入口测试失败前后磁盘与 revision 不变；类型化错误映射精确字段，非类型化错误用固定安全文案。
- [x] 运行上述用例并观察 RED；实现统一 writer，逐个接入所有 API HTTP 失败出口。保留已有调用者需要的 error/errors 别名到兼容期，但值同样脱敏。不得把 200 作业失败改成 HTTP 成功诊断的丢弃或把流式下载整体缓存。
- [x] 对未知 API 路径使用 JSON 404，非 API 静态路由保持原行为。处理 body limit 返回 JSON 413；审查中间件顺序使认证和 CSRF 也走该 writer。
- [x] 全量 Go 与相关 handler race 通过后提交。

## Task 3: Web 字段定位与可访问反馈

**Files:** 修改 `backend/web/app.js`、对应 CSS 与 `web/` 镜像；新增 `tests/web-field-errors.cjs` 并接入现有测试脚本。
**Interfaces:** `api()` 抛出的 Error 保留 `code`、`fieldErrors`、`status`；`field()` 接收可选完整 `configPath`，默认由 section.path 与局部 path 组合；数组编辑器显式传入下标路径。表单展示函数只用 textContent。

- [x] 浏览器先覆盖真实 API 的无效静态地址保存：保持输入/dirty、字段 aria-invalid、可访问错误文本和焦点；修正后保存成功且错误清除。
- [x] 真实第二行数组错误只标记对应行；受控错误注入覆盖不可见字段、恶意 HTML 文案、旧响应格式、409 无字段错误。
- [x] 观察 RED 后实现路径映射、aria-describedby、行内提示和第一个可见错误的焦点。隐藏/其他页面字段使用页级提示，不能自动导航丢失草稿。
- [x] 两套资源 cmp、全量浏览器与 Go 通过；独立整分支审阅一次，重要发现按 RED→GREEN 修复。记录实际验证范围后提交。

## Rulings and coverage

- 完整设计和自动实施方式已经获授权；这是现有错误契约要求的实施细化，不重新征求平台或流程选择。
- 采用类型化字段路径而非解析既有英文错误：后者依赖文案且无法可靠区分数组项。先保持既有校验语义，避免把错误展示改造变成隐含规则变更。
- 本计划不宣称解决配置副本迁移、显式清空秘密、浏览器任意精度插件数值、PPPoE/IPv6 或插件运行生命周期。
- 自审：Task 1 提供 Task 2 消费的 ValidationError；Task 2 输出 Task 3 消费的字段字典；五类 Review Focus 分别由三个任务指定测试覆盖。正式执行前检查 API 出口清单，防止仅替换配置 handler 就误报全 API 完成。

## Execution ledger

- Task 1 已实现类型化 `ValidationError`、固定安全消息和数组下标路径；既有有效/无效判定保持。DDNS 缺项分别指向具体必填字段。
- RED：7 类原有校验返回普通 error，部分带输入值；新增 schemaVersion/revision 两类也观察到缺少类型化路径。GREEN：config 全包通过，最终 config race 通过（1.139s）。
- 全量 Go 在核心字段改造后通过（`/tmp/router-field-errors-go.log`，server 28.713s，退出 0）；随后仅两处版本元数据错误改为相同类型，并经 config race 验证。
- Task 1 尚未提交；Task 2 API 结构和 Task 3 Web 定位尚未开始，不宣称用户端错误契约完成。
- 提交隔离：当前 Git index 仅为上一配置契约快照。字段错误改动位于未暂存 `types.go`、`document.go` 和新增 `validation.go`/`validation_test.go`；不得使用 `git add -u` 将两项混合。上一轮最终 VM 运行会话 65402，日志 `/tmp/router-config-contract-vm-final.log`，不覆盖本任务新代码。

- Task 2 初步接线：配置 PUT/恢复/客户端校验和 managed apply 使用 writeConfigError；HTTP 失败经统一 writer 或 writeJSON 规范化，保留 legacy error/errors 和 endpoint metadata。未知 API 404、CSRF、错误方法使用 JSON；静态资源继续原 HTTP 行为。
- 请求体统一限制 16 MiB（与草稿读取上限一致），认证/CSRF 在读取前执行；已知长度与分块传输均受限。该上限为本实现选择，不改变文件迁移格式。
- RED：TestAPIErrorContract 7 类请求均失败于旧格式或错误状态。GREEN：该用例通过；补充认证优先级、分块超限、存储失败脱敏、旧 metadata 保留测试，与配置修订测试一同 race 通过（2.959s）。全量 Go 通过（`/tmp/router-api-errors-go.log`，server 36.475s）。
- Ruling: HTTP 错误规范化在 writeJSON 处理现有 map，并由 writeAPIError 显式构造新错误；不强制转换成只含三字段的 struct，以免丢失 jobId、available 等已有调用者元数据。仅 status >= 400 处理，200 作业结果不改写。
- Task 2 尚需最后出口审查、调用者兼容和完整浏览器验证；Task 3 未开始。新增 api_errors.go/api_errors_test.go，以及 server.go/managed.go 的本任务差异均未暂存；上一配置契约仍只提交原 index 快照。

- 上一配置契约已提交为 020f17f，四 VM 最终通过，后续本计划无需继续保护旧暂存区；当前未提交差异均属于字段错误工作（索引与缓存除外）。
- Task 3 已实现：api 保留 code/fieldErrors；表单绑定 schema 路径，行内错误 aria-invalid/aria-describedby、首个可见错误聚焦、不可见字段页级提示。输入事件只清除当前字段错误；错误以 textContent 展示。
- Ruling: field() 从当前 section.data 的对象身份解析局部路径，再组合 section.path，支持嵌套对象与数组；保留 configPath 显式覆盖选项。无需为每个既有 collection 手写下标映射，删除/新增时重绘会重新计算路径。当前配置来自 JSON，无循环引用。
- Ruling: 新浏览器场景加入现有 web-save-apply.cjs，共用真实 API fixture，避免新文件重复启动账号/服务。首个真实保存失败用例先观察 aria-invalid 缺失，再实现通过；数组定位和恶意文本/独立清除补充回归也通过，3/3（`/tmp/router-field-errors-browser-focused.log`）。
- 完整浏览器运行日志 `/tmp/router-field-errors-browser-all.log`；工具会话待后续查询。独立审阅 agent field_errors_review 已启动，只读当前变更。
- 临时浏览器二进制构建因环境 Git VCS 元数据读取失败而使用 -buildvcs=false；只影响临时测试二进制版本戳，不更改正式 Nix 构建配置。

- 独立整变更审查完成：无 Critical，1 项 Important（managed apply 的 legacy error 码被文案替代）。回归读取真实坏草稿触发 apply，观察 400 的 error 不再等于 invalid_config 后修复；现在 code/error 保留 invalid_config，message/fieldErrors/errors 保留详情。针对性测试通过。
- 审查前完整浏览器 38/38 通过（`/tmp/router-field-errors-browser-all.log`，78.462s）。修复后最终完整 Go 日志 `/tmp/router-field-errors-go-final.log`，工具会话 83770；最终浏览器日志 `/tmp/router-field-errors-browser-final.log`，工具会话 90327。后续先查询句柄和日志再判断终态。

### 最终验证与提交决议

- 最终全量 Go 退出 0，server 31.682s；go vet 退出 0。
- 最终定向 race：config 1.037s、server 2.249s，退出 0（`/tmp/router-field-errors-race-final.log`）。
- 最终浏览器 38/38 通过，74.694s，退出 0（`/tmp/router-field-errors-browser-final.log`），包括新字段反馈与既有全部场景。两套 Web JS/CSS 一致，diff check 通过。
- Ruling: 三个任务作为完整用户反馈闭环统一提交；分层工作已各自 RED→GREEN，提交不留仅后端或仅前端中间版本。上一配置契约提交 020f17f 独立保留。
- 本次未重新执行四套 VM：其最新已通过结果属于 020f17f 配置契约；本次新增的 HTTP 错误和 Web 字段反馈由真实 API/浏览器和 Go 验证，不用旧 VM 结果冒充覆盖新代码。
- 未部署实体 gw；完整目标中的秘密显式清空、插件副本与任意精度、PPPoE/IPv6/访客等仍待实现，推送/PR 凭据阻塞另行追踪。
