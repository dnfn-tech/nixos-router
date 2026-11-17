# 密钥读取、保留与显式清空实施计划

> 使用 superpowers:executing-plans，沿用已批准设计与自动实施方式。

**Goal:** 默认 API 不返回插件秘密；保存保留既有秘密且支持明确清空。
**Spec:** `../specs/2026-10-15-usable-home-router-design.md` 第 3 节。
**Architecture:** 配置修订绑定的 opaque secret references 支持插件数组重排；If-Match 仍是写入前置条件。clearSecrets 请求元数据单独表示清空，不写入持久配置。
**Tech Stack:** 既有 Go、JSON、原生 Web；不引入依赖。

## Global Constraints

不操作实体 gw 或宿主网络；不新增 LAN 改址超时回滚。两套 Web 保持一致。默认留空/**** 保留语义不改变。清空后仍须通过配置校验，活动 PPPoE/DDNS 必填秘密不能被静默删空。

## Review Focus

- 默认 GET 的插件嵌套 map、数组、秘密对象均不泄露，脱敏不能修改服务内存。
- 数组重排不得用旧下标串换凭据；失效引用拒绝，不能把标记持久化。
- 清空和过期修订同请求时拒绝且不落盘；批量清空中有非法路径时整批拒绝。
- 插件键含点、斜杠等字符时路径无歧义；普通字段不能通过 clearSecrets 删除。
- Web 清空可撤销，保存失败仍保留清空意图；新增/删除数组项后不误清其他项目。

## Task 1: 默认读取与保留链路

Files: 新建 config/secrets.go、secrets_test.go，修改 Config.RedactedCopy、server 保存/插件/恢复入口及真实 API 回归。
Interfaces: IsSecretKey(string) bool；ResolvePluginSecrets(old Config, incoming map[string]PluginConfig) (map[string]PluginConfig,error)。默认插件 secret value 输出修订和路径派生的不透明引用，秘密值不参与摘要；当前修订下构建引用表。引用只能进入秘密字段，不能经普通字段还原后泄露。引用不等于登录权限；仍使用既有认证与 If-Match。

- [ ] 默认 GET 暴露插件秘密的回归先 RED，再实现递归脱敏。
- [ ] 数组重排后还原正确秘密；未知/其他修订引用拒绝，错误不包含引用或秘密；脱敏与解析均不修改输入对象。
- [ ] 三类配置写入口先验证修订，再解析引用，再合并保留值。插件 secret key 判定共用一个 helper。
- [ ] Go、真实浏览器回归通过；测试读取实际存储值须显式用非脱敏接口/备份，不能误将正常脱敏视为写入丢值。

## Task 2: 独立清空请求

Files: server/config_write.go 与测试、config/secrets.go。
Interface: PUT /api/v1/config 支持 envelope {config, clearSecrets: [["wan","pppoe","password"]]}；路径为字符串段数组，避免插件键歧义。旧 raw/envelope 读取保持；持久 Config 不添加命令字段。

- [ ] 测试旧留空保留、合法显式清空、不能清空 hostname/整个插件对象、非法下标/重复路径、部分非法整批拒绝、过期 If-Match、清空必填秘密校验失败；观察 RED。
- [ ] 合并旧值后在独立候选中清空白名单核心秘密和已识别插件秘密，再 Validate/原子保存；失败不发布内存或磁盘。最多 256 个路径，每段最多按配置现有限制，错误不回显自定义路径。
- [ ] POST restore 继续只接受备份文档，不从备份中执行清空命令。单插件接口可保留旧语义，明确清空统一走唯一 config PUT。

## Task 3: Web 显式意图与验证

Files: 两份 app.js/styles.css、web-save-apply.cjs、landing/testing/status 文档。
- [ ] 密码控件提供“清空已保存密钥”独立选项，显示待清空状态并支持撤销；输入新值撤销清空。集合操作通过对象身份在提交时计算路径，不保存易错的旧数组下标。
- [ ] 高级插件 JSON 额外列出当前脱敏秘密供选择清空；不要求用户手写保留标记或清空协议。清空动作只在保存成功后从草稿移除，冲突保留。
- [ ] 真实浏览器验证留空保留、清空/撤销、校验失败、重排后正确清空；全量 Go/vet、race、浏览器，独立审阅一次后提交。

## Rulings

完整设计已批准；以上为工程细化，不重新征求已授权选择。发现默认 API 插件未脱敏与数组保留不完整，是显式清空的前置修复。采用修订绑定引用而非按下标匹配，因为现有高级编辑器明确支持数组重排。引用使用保留命名空间；不作为授权凭证。完整目标其他功能保持独立追踪。

## Execution ledger

- 分支 feat/20261109-secret-intent，基线 a0673f7。当前未提交的实现属于此计划，不修改原始用户 worktree。
- Task 1：确认 Config.RedactedCopy 原来只处理核心秘密，插件没有脱敏。回归观察真实泄露后增加 config/secrets.go：按同一 IsSecretKey 规则递归脱敏；引用为 SHA256(revision + JSON path)，不含秘密值，保留命名空间 `__router_secret_ref_v1_...__`。
- ResolvePluginSecrets 仅使用当前草稿生成的引用表，只向秘密字段还原，未知/过期引用与向普通字段复制引用均拒绝；返回深拷贝。PUT config、POST restore、PATCH plugin 在 If-Match 检查后调用。
- 三入口 API 回归先观察引用被写入磁盘，再接线通过。嵌套数组重排、秘密对象脱敏、源对象不变、失效引用和公共字段拒绝均覆盖。全量 Go 退出 0（server 28.783s，`/tmp/router-secret-intent-go.log`），定向 race config 1.039s/server 1.331s 通过。
- 浏览器连续保存回归先失败于旧引用；包含插件配置的页面成功保存后重新读取并绑定编辑器，保证 advanced JSON 闭包也拿到新修订引用。数组重排加连续保存已通过（`/tmp/router-secret-intent-browser-green.log`）。测试 fixture/存储断言的 configOf 显式读取 redact=0；真实页面仍使用默认脱敏读取。
- 完整浏览器正在运行：会话 99873，日志 `/tmp/router-secret-intent-browser-all.log`。先查询原句柄，不重启。
- Task 1 尚须检查非活动 PPPoE/DDNS 凭据：现有 mergeSecrets 按 mode/provider 条件保留，切换模式可能把隐藏字段的 **** 写回。要用回归确认并修复，不能仅完成插件分支。
- Task 2/3 显式清空 API 和控件尚未实现；未提交，未宣称整个密钥契约完成。后续仍需独立整变更审阅。

- 上一插件脱敏完整浏览器已终态通过 38/38，90.244s（`/tmp/router-secret-intent-browser-all.log`，会话 99873 已退出 0）。
- 非活动核心凭据回归确认旧 mergeSecrets 按 WAN mode/DDNS provider 丢失占位值；修复为各凭据字段独立保留，不随当前模式/服务商选择删除。TestModeSwitchPreservesInactiveSecrets 已 RED→GREEN。
- Task 2 已接入 decodeConfigWrite：仅 PUT config 支持 clearSecrets 段数组 envelope；其他旧 raw/envelope 走原解析。ClearSecrets 在解析引用、合并保留值后编辑独立候选，再校验和原子保存；最多 256 路径、每路径 64 段、每段 1024 字节，拒绝重复/非法路径。
- TestExplicitSecretClearingIsAtomicAndConditional、TestClearingRequiredSecretFailsValidation 已 RED→GREEN，覆盖留空保留、过期拒绝、部分非法整批不变、普通字段拒绝、无效数组下标、重复路径、插件含点/斜杠键、合法清空及活动 PPPoE 校验失败。
- 当前全量 Go 会话 40625，日志 `/tmp/router-secret-clear-go.log`；当前定向 race 会话 78749。后续先查询原句柄。Task 3 控件尚未实现；全部密钥工作未提交，需完整浏览器最终验证及一次独立审阅。

- 上述全量 Go 会话 40625 已退出 0；定向 race 会话 78749 已退出 0（server 1.616s）。下一步为 Task 3 UI，不需重复启动这两轮。

- Task 3 本轮进展：普通密码控件清空/撤销/替换取消已实现；高级 JSON 提供按引用绑定的清空选择，重排后重新计算路径；待清空引用被复制时拒绝歧义保存。两份 Web 同步。
- 四项定向浏览器通过（/tmp/router-secret-clear-focused.log，7.756s）；完整保存/应用 36/36（/tmp/router-secret-clear-browser-final.log，81.845s），managed 能力 6/6（/tmp/router-secret-clear-capabilities.log，8.036s），合计42。首个命令误写能力文件名，已用正确 web-managed-capabilities.cjs 单独补跑，不将首轮视为完整能力覆盖。
- 全量 Go 通过（/tmp/router-secret-final-go.log，含缓存）；go vet 与 diff --check 通过；定向 race config/server 通过（/tmp/router-secret-final-race.log，1.039s/3.760s）。新增数字字面量保真回归直接通过，确认 Config.UnmarshalJSON 的 UseNumber 已保护大整数/指数/高精度小数，无需修复。
- 独立 reviewer secret_intent_review 已完成一次整变更只读审阅：无 Critical；Important 待修复：mergePreserveSecretsMap 对数组直接替换，数组内空串/**** 可绕过显式清空并持久化。须补真实 API/UI 回归并处理身份不明情况，不能按旧下标恢复；也不能简单禁止所有空值而破坏已清空条目的后续保存。当前未提交，密钥契约尚未完成。
- Final: minor (deferred): 高级 JSON 复制待清空引用触发校验后，取消清空复选框不会立即重算 textarea validity，需再次编辑 JSON 才解除；保留作后续 UX 修整，未称已解决。

- Final 修复中：TestArraySecretPlaceholdersPreserveOnlyUnambiguousItems 已观察 RED（placeholder erased existing secret），随后 GREEN。通过全部非秘密字段的序列化形状唯一匹配旧数组项目，保留空串/****/null 的旧值，不使用旧下标；有多个或无匹配且旧数组非空时拒绝歧义。匹配表按形状构建，避免逐项全数组扫描。嵌套数组继承外层歧义。
- Task 3: Ruling: 泛型插件数组没有稳定主键，空占位且公共字段修改时不能猜测身份 — 拒绝并要求保留引用或新值；保留引用的重排/改名仍可保存。代价是手写 API 的模糊空值更新需要重新读取引用，不以潜在串换凭据换取兼容。
- 新增真实 API 空值不改变磁盘秘密、重复项目及嵌套歧义、已清空项目继续保存的回归。最终全量 Go 会话70030（/tmp/router-secret-array-go.log）、浏览器会话59421（/tmp/router-secret-array-browser.log）仍须查询终态；定向 race 已退出0，config 1.078s/server 6.666s；vet/diff --check 已通过。

- Final: fixed 数组空占位可绕过显式清空 — TestArraySecretPlaceholdersPreserveOnlyUnambiguousItems RED→GREEN；真实 API 和浏览器回归通过。全量 Go 退出0（server34.680s），浏览器43/43、74.686s（会话59421退出0），定向race/vet通过。本改造未重新跑VM，不复用旧VM结果声称新提交已验收。
- 独立审阅已完成一次，Important 已修复；Minor 保持上述登记。Task1/2/3实现与本地验证完成，等待本地提交；完整家用路由目标仍包括PPPoE、IPv6、访客与高级服务/插件等工作。
