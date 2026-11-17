# 内置插件与功能模块

插件实现位于 `backend/internal/modules/`。当前支持编译进二进制的一方模块，不支持在线安装、动态 Go 插件、第三方子进程或任意命令钩子。

## 当前支持

| 模块 | 归属与能力 |
|---|---|
| AdBlock | 路由器管理；支持托管模式实际应用及仅生成模式文件输出 |
| Mihomo、Tailscale、ZeroTier | 系统管理；保留历史配置，不通过本项目启停服务 |
| Traffic、VLAN | 运行期未实现；不提供可成功启用的开关 |
| 未知历史模块 | 禁用配置可保留、编辑和导出；不能启用或执行 |

IPv6 是核心网络功能，使用 `ipv6` 配置，不受 `plugins.ipv6` 开关控制。其他核心服务同样保持唯一配置入口。

## 注册与权限

每个已注册模块声明 ID、版本、标题、导航、配置 schema、能力要求及资源归属。ID 和导航路径冲突会被拒绝。模块通过纯校验和 DNS 配置贡献接口接入核心，不能访问 root 命令执行器。

NixOS 托管部署通过 `services.nixos-router.backend.managed.modules` 设置允许清单，默认 `[ "adblock" ]`，空清单禁止所有可选模块。该清单由 NixOS 同时传给 API 和 executor，不属于可编辑 HTTP 配置。两端分别校验，不能通过直接 RPC 绕过部署限制。

托管模式的生命周期为：校验完整配置及依赖 → 生成候选代际 → dnsmasq 配置预检 → 核心事务激活 → 持久化 last-good → 发布已应用配置。停用也是一次完整应用，仅移除模块拥有的配置。失败反映在作业终态，并使用已有恢复机制；不新增 LAN 改址倒计时回滚。

仅生成模式不会应用运行状态。旧的非托管 reload 路径不支持插件运行应用，会拒绝启用的模块，而不是把生成成功报告为生效。

## AdBlock 配置

```json
{
  "plugins": {
    "adblock": {
      "enable": true,
      "config": { "entries": ["ads.example.com", "tracking.example.org"] }
    }
  }
}
```

- 依赖 `dns.enableDnsmasq=true`，否则保存/应用前拒绝。
- 最多 4096 条 ASCII DNS 域名；标签最多 63 字节，规范化后域名最多 253 字节。
- 大小写规范化、去掉末尾根点并去重；不接受 URL、IP、通配符、原始 dnsmasq 指令或换行。
- 规则覆盖域名及子域，同时阻断 A/AAAA。客户端若绕过网关 DNS，不属于该模块的拦截范围。
- 订阅下载、自动刷新、命中统计及 AdGuard Home 集成未实现。
- 旧 `config.lists` 禁用时保留；非空列表会阻止启用，页面可明确清空。不会静默忽略或下载。
- 禁用并应用后，规则从当前 DNS 代际移除；正常上游解析恢复。

## API 与页面

`GET /api/v1/plugins` 返回清单，`GET /api/v1/plugins/{id}` 返回单项。清单包含 `available`、`runtimeSupported`、`ownership`、`configSchema`、期望 `enabled`、可用时的 `appliedEnabled`/`appliedRevision` 及 `status`。

`status=service-active` 仅说明确认配置已应用且托管 DNS 服务观测为 active，不代表每个客户端查询已经实测。未能观测时显示 unknown，不从草稿开关推断运行状态。

主配置写入和模块 PUT/PATCH 均保持 revision 并发校验及原有秘密引用约束。模块错误给出 `plugins.<id>...` 字段路径，不回显秘密。未知或系统管理模块不能启用；历史禁用配置不会阻断基础路由。

前端保留原生 JavaScript 框架；能力和菜单可见性来自注册表 API。扩展管理页保留不可用项及原因，详细导航只呈现可用模块。`web/` 和 `backend/web/` 必须同步。

## SSH

SSH 完全由 NixOS `services.openssh` 和系统用户公钥配置管理。本项目不提供 SSH 编辑页面、草稿设置或配置型运行状态，不写 sshd 配置或启停服务。

配置 schema v2 移除 `ssh`。旧文件与备份兼容读取，编辑草稿首次迁移保存原始字节的私有 `.v0.bak`/`.v1.bak`；读取 last-good 不改写原文件。当前配置写入携带 `ssh` 将得到字段错误。

系统声明的实际 SSH 端口仍作为防火墙管理面策略输入，包含显式 ListenAddress 端口。SSH 关闭时不假定 22 端口；访问边界不会因移除配置入口而放宽。

## 验证入口

- Go：`cd backend && go test ./...`。
- 浏览器：`tests/package.json` 的 `test:web`，需要测试后台二进制和 Chromium。
- 模块契约：`nix eval --impure --json --file tests/module-managed.nix`。
- 实际 DNS 生命周期：`nix build .#checks.x86_64-linux.vm-managed-adblock -L`。

VM 检查由真实 API 配置/应用，客户端验证上游 A/AAAA、域名与子域拦截、无关解析、非法配置拒绝、规则替换、执行器重启、整机重启及停用恢复。测试定义不等同于本次运行结果，结果另见验收记录。
