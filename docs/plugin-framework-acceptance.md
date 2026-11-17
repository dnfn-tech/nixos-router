# 插件框架与系统 SSH：本地验收记录

日期：2026-11-12。基线为 `f7905d02bd6c3cf38aedbb737cdaa31fd9647d60`，变更保留在本地隔离 worktree，尚未提交或发布。实体 gw 未部署、未执行硬件验收。

## 已实现

- SSH 从当前配置模型、编辑页面和配置型状态中移除，schema v2 兼容读取旧配置/备份/last-good；编辑草稿迁移保留原始字节备份，新写入 SSH 字段明确拒绝。
- SSH 服务、公钥和认证策略由系统管理。防火墙仍使用 NixOS 声明的实际监听端口，SSH 关闭时不假定 22。
- 编译内置模块注册表提供唯一标识、版本、导航、配置 schema、能力和归属声明；API/executor 使用相同部署允许清单。
- AdBlock 首先接入托管 DNS 事务，支持域名及子域的 A/AAAA 拦截、替换、停用、失败恢复及重启恢复；订阅下载未实现。
- 导航和模块能力展示由注册表 API 驱动，新模块不需要先有配置或前端硬编码页面。系统管理或未实现的模块不能通过开关冒充运行支持。
- 秘密引用、revision 并发校验和历史禁用配置保留机制继续使用既有契约。新增或修改的模块 ID/已知配置结构必须校验，原样历史禁用内容可兼容保留。

## 本次实际验证

| 检查 | 结果 |
|---|---|
| Go 完整测试 | 通过 |
| Go vet | 通过 |
| Go 完整 race | 通过，server 包 428.879 秒；使用 `-timeout 30m` |
| 浏览器完整套件 | 50/50 通过 |
| 两组 Nix 模块契约 | 全部断言为 true |
| AdBlock VM | 通过，114.07 秒 |
| 有线路由 VM | 通过，234.79 秒 |
| IPv6 VM | 通过，355.78 秒 |
| 权限隔离 VM | 通过，66.20 秒 |
| WiFi7/80MHz/WPA3 双网段 IPv6 VM | 通过，438.52 秒 |
| 隔离 nixos-home gw 系统构建 | 通过；未激活 |
| 两份 Web 资源同步、JS 语法及 diff 空白检查 | 通过 |

完整 race 首次在并行构建/VM 负载下触及默认 10 分钟累计时限，堆栈位于 bcrypt 密码校验；保留全部断言和真实密码强度，以更长总时限重新执行后通过，没有数据竞争报告。

独立审查发现一项 P2：禁用配置的兼容分支也放过新写入非法内容。已用 `TestDisabledModuleEditsStillValidateNewData` 先失败、修复后通过的回归覆盖，并在 API 和 managed 应用边界共享校验。

## 证据及版本绑定

本地材料目录：`/tmp/router-plugin-acceptance-20261112/`，包含 `results.json`、完整日志、模块断言、源码归档/逐文件 SHA256 清单及 6 项已注册 Nix store 输出信息。构建和 VM 使用 `/tmp/router-plugins-verified-source` 固定源码快照；运行代码及浏览器测试与工作区一致，后续差异仅为验收文档和计划状态。

最终 gw 系统产物：

`/nix/store/z15wzmyss37i1v627szq0bqk630nc996-nixos-system-gw-26.05.20260825.f4f6986`

集成构建使用已有私有 nixos-home 隔离快照，以 `--override-input nixos-router path:/tmp/router-plugins-verified-source --no-write-lock-file` 替换输入；未修改原始 nixos-home 工作区。

本轮按影响运行上述五组 VM。未重新执行 legacy vm-router、PPPoE/PPPoE6、独立 WPA2/WiFi6/WiFi7 或不含无线的双网段 IPv6 套件，不把上一候选的全套结果算作本次通过。

VM 无法替代实体 MT7927 射频、CN 信道、吞吐、真实运营商线路及现场迁移验收。插件支持范围以 [plugins.md](plugins.md) 为准，不代表其他可选插件已实现。
