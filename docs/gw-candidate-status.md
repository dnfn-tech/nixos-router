# gw 软件候选交付状态

核对日期：2026-11-17。本文补充此前验收记录，不改变历史结果的日期和覆盖范围。实体 gw 尚未部署或验收。

## 源码与本地验证

候选分支为 `feat/20261109-managed-ipv6`。本次提交包含系统 SSH 归属调整与插件基础框架，具体 revision 以本文件所在提交的 `git rev-parse HEAD` 为准。设备配置、驱动及秘密仍由 nixos-home 管理。

对 `/tmp/router-plugin-acceptance-20261112/source-manifest.json` 的 224 个文件逐项核对，固定快照零差异；提交前工作区仅有验收文档差异。现有证据包的 SHA256SUMS 全部通过。旧记录仍见 [插件框架验收](plugin-framework-acceptance.md)，功能要求与硬件步骤分别见 [集成对照表](gw-integration-matrix.md) 和 [硬件验收方案](gw-hardware-acceptance.md)。

本轮重新执行：

- 完整 `go test -mod=readonly ./...`：退出 0；日志 `/tmp/router-goal-20261117-go.log`。
- 完整 `go vet -mod=readonly ./...`：退出 0；日志 `/tmp/router-goal-20261117-vet.log`。
- 完整浏览器回归：50 项通过，0 失败、0 跳过；日志 `/tmp/router-goal-20261117-browser.log`。使用本机 Chromium 和此前由同一运行源码构建的隔离测试二进制。首次缺少 Playwright 下载的浏览器，不计为通过；指定本机 Chromium 后完整重跑。
- `git diff --check`、两份 `app.js` 一致及 JS 语法检查：通过。

## 尚未完成的交付门槛

1. 在本次运行源码上补齐 PPPoE/PPPoE6、独立双网段 IPv6、WPA2/WPA3、路由式无线及 WiFi 6/7 回归。已启动八组隔离 VM，日志 `/tmp/router-goal-20261117-vm.log`；本文编写时尚未终态，不计作通过。
2. 以本次固定提交重新绑定 nixos-home 输入并构建，归档源码 bundle、私有集成快照、摘要及复现脚本。此前 path 快照构建不能冒充本次固定提交的构建证明。
3. 完成远端分支及 PR。只读查询确认该分支尚无 PR；当前 GitHub OAuth scope 为 `gist, read:org, repo`，缺少 `workflow`。此前 SSH 签名/推送失败须按当前凭据重新核查，不能声明已发布。

本页记录进度，不宣告最终候选验收完成。硬件射频、实际 WAN、真实端口映射和吞吐仍由用户审核后决定是否测试。
