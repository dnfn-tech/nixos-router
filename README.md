# nixos-router

为 NixOS 打造的“家用路由器式”管理模块与参考实现（docs-first）。目标是在局域网（LAN）内通过 Web UI 配置网络与系统功能，“保存并应用”即可生效；配置采用单一真源，运行态分离，具备可备份/可回滚的工程实践。

> 当前为文档与模块骨架阶段：WebUI/后端与 apply 管道尚未实现，`modules/` 为占位。欢迎基于文档讨论与贡献。

```nix
# flake.nix
inputs.nixos-router.url = "github:dnfn-tech/nixos-router";
```

在主机配置中导入 `inputs.nixos-router.nixosModules.default`（当前为空壳模块，供占位/试配）：

```nix
{ inputs, ... }:
{
  imports = [
    inputs.nixos-router.nixosModules.default
  ];
  # 预期将提供如下（示例）选项，现阶段尚未实现：
  # services.nixosRouter.webui.enable = true;
  # services.nixosRouter.webui.port = 8080;
  # services.nixosRouter.stateDir = "/var/lib/nixos-router";
}
```

## 简介

- 单一真源（Source of Truth）：`/var/lib/nixos-router/config.json`
- 运行态与审计：`/var/lib/nixos-router/state.db`（SQLite）
- 以 dnsmasq 作为 DHCP + DNS 的首选后端；默认单一逻辑 LAN：`br-lan`
- Web UI + API + apply agent 仅在 LAN 监听（设计），远端 `nixos-rebuild` 不触碰 `stateDir`
- 稳定核心 + 模块/插件架构：核心页面固定存在，功能以模块/插件提供

## 特性（设计目标）

- “保存并应用”的局域网内管理体验；原子写入 + 作业编排，失败不破坏当前生效配置
- 模块化/插件化：QoS、家长控制、DDNS、IPv6、广告过滤（AdBlock）、流量统计（Traffic）、代理（Mihomo）、VLAN、Tailscale、Zerotier 等
- WiFi 与有线同一逻辑 LAN（AP 接口桥接进 `br-lan`），能力驱动并发（单 AP/多 AP/DBDC）
- 安全默认值：WAN 入站拒绝；UI 仅 LAN 可达；会话 + CSRF 防护；审计开箱即用
- 配置可备份/回滚；敏感字段仅存放于本机 `stateDir`，可提供“脱敏导出”

## 当前状态

- 文档已合并，确定 v1 的模型与接口边界（见“文档”一节）
- WebUI 与后端（API/Apply）尚未实现；NixOS 模块为占位
- 欢迎以 docs-first 的方式协作：schema/校验、运行时配置生成器、API 草图等

## 架构概览

- 组件：静态 WebUI、API（LAN-only）、Apply Worker（串行执行 apply）
- 数据：`config.json`（意图配置，SoT）+ `state.db`（会话/审计/作业/可选修订）
- 运行时：生成 nftables/dnsmasq/hostapd/ppp 等配置并按序 reload/restart
- 插件：稳定核心 + 功能模块；导航与页面由模块贡献并统一汇总
- 远程接入示例（可选插件）：Tailscale 支持三种控制面模式
  - `official`（官方控制面）
  - `selfhost`（外部 Headscale）
  - `headscale`（本机运行 Headscale，谨慎仅对 LAN 放行管理端口）

详见 `docs/backend-architecture.md`。

## 环境要求

- 目标平台：NixOS 主机（物理或虚拟），建议具备 WAN/LAN 物理口（或以 VLAN 区分）
- 单一逻辑 LAN：桥 `br-lan`（多物理口桥接）；WiFi（如有）桥接入 `br-lan`
- DHCP/DNS：以 dnsmasq 为主（v1 不引入 Kea 作为常规路径）
- 目标主机注意事项见 `docs/target-host-notes.md`

## 快速开始（占位）

> 目前尚未发布可用的 WebUI/后端与 apply 实现；以下用于接入未来的模块选项。

1. 在 `flake.nix` 增加 `inputs.nixos-router`
2. 在主机配置导入 `inputs.nixos-router.nixosModules.default`
3. 等待后续版本提供 `services.nixosRouter.*` 选项以启用 WebUI/API/Apply

## 配置（设计草案）

预期将提供以下（示例）入口：

```nix
services.nixosRouter = {
  webui.enable = true;
  webui.port = 8080; # LAN-only
  stateDir = "/var/lib/nixos-router";
};
```

具体 schema/接口与运行时生成规则以文档为准。

## 文档

- 需求与信息架构：`docs/requirements.md`
- 后端服务架构：`docs/backend-architecture.md`
- 插件与功能模块：`docs/plugins.md`
- 存储选型（JSON + SQLite）：`docs/storage-choice.md`
- 测试计划：`docs/test-plan.md`
- 目标主机说明：`docs/target-host-notes.md`

## 开发

- 首选 Go（静态编译、部署简单，SQLite 生态成熟）；可评估 Rust/Python 作为备选
- 建议从以下方向切入：配置 schema 与校验、生成器（nftables/dnsmasq/hostapd 等）、最小 API 与作业管道
- 欢迎以小步 PR 推进：文档改进、接口草图、模块选项占位与验证

## 路线图（提要）

1. 基础 schema 与只读 UI
2. 可写核心面（WAN/LAN/DHCP+静态、DNS、防火墙+端口转发、WiFi、DDNS）与最小 apply 管道
3. 认证与审计（会话、CSRF、密码策略、审计检索）
4. 高级服务（家长控制、QoS、UPnP、IPv6 细节）
5. 系统级操作（备份/恢复、日志、重启、自愈与回滚）

详述见 `docs/requirements.md` 与 `docs/backend-architecture.md`。

## 安全说明

- 管理界面仅在 LAN 监听；防火墙自动放行该端口的 LAN 流量
- WAN/IPv6 默认不开放管理入口
- 会话采用 Cookie（HttpOnly、SameSite）+ CSRF 防护；密码散列（argon2id/bcrypt）
- 变更/应用/备份/恢复均写入审计日志

## 许可

本项目采用 Apache License 2.0（Apache-2.0）开源许可，详见 `LICENSE`。

Copyright 2026 dnfn-tech
