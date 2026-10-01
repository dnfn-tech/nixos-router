# nixos-router

家用路由器 Web 管理 · NixOS 通用模块（docs-first）

## 简介（About）
nixos-router 是一个可复用的 NixOS 模块，目标是在“家用路由器”设备上提供基于浏览器的本地管理体验：在 LAN 内编辑配置，保存并即时应用，而不是把远程 `nixos-rebuild` 作为日常路径。适合自架家庭网络 / homelab / 轻量网关场景。

## 特性（Features）
- 核心能力（设计已定，实施按里程碑推进）
  - 单一逻辑 LAN：有线口与 WiFi AP 接口桥入 `br-lan`；概览为“扁平网口”（WAN/LAN 并列），不展示拓扑
  - WAN：DHCP / 静态 / PPPoE；NAT；IPv6（DHCPv6/SLAAC/PPPoE+IPv6）
  - LAN + DHCP/DNS：dnsmasq（池、静态绑定、内网域名、上游转发）
  - WiFi：按硬件能力裁剪并发（如 `maxAP=1` 仅单 AP；支持时并发 2.4G+5G）；访客网络默认用额外 BSS 并与 `br-lan` 隔离
  - 防火墙 + 端口转发（nftables）；UPnP（miniupnpd 可开关）
  - 设备列表、SSH、系统（备份/恢复/审计/重启）
  - 保存/应用状态机：validate → 原子写 → 生成配置 → reload/restart → 审计；失败不破坏现网并可回滚
- 插件/功能模块（可选，启用后出现导航与页面，禁用则隐藏并在应用时停止/撤销）
  - QoS、家长控制、DDNS、IPv6、AdBlock（dnsmasq blocklists）、Traffic（vnstat/nft 采集）
  - Mihomo（Clash Meta 代理，含 fake-ip/redir-host 注意）、VLAN（多 VLAN/LAN，默认关闭则保持单一 `br-lan`）
  - Tailscale（三态：official | selfhost | headscale 本机控制面）、Zerotier（official | selfhost）

## 当前状态（Status）
- 早期阶段：以“文档优先（docs-first）”推进，提供需求、架构与测试计划；模块目录为占位/stub；WebUI 与后端尚未实现。
- flake 与模块入口已存在，便于后续演进与集成。

## 架构概览（Architecture · brief）
- 管理面仅在 LAN 监听；不提供 WAN 侧管理
- 存储：`/var/lib/nixos-router/config.json` 作为“意图配置”单一真源；`state.db`（SQLite）保存会话/审计/作业等运行态
- 推荐技术栈：Go API 服务 + Apply Worker（串行作业）
- 插件化：功能以模块/插件实现；UI 导航与页面由插件声明并由后端汇总
- NixOS 模块负责安装与开机再应用；远程更新不会清空 `/var/lib/nixos-router`
- 详见 `docs/backend-architecture.md`

## 环境要求（Requirements）
- NixOS 主机（启用 flakes）；x86_64 / aarch64 路由器级硬件
- 首个目标主机参考：`docs/target-host-notes.md`

## 快速开始（Quick start）
> 当前为 docs-first 阶段：下述模块导入示例可用，但 WebUI/后端尚未实现；`services.nixosRouter.webui.*` 等选项在架构文档中已设计，实际启用路径将在后续提交中落地。

1) 在 flake 中添加输入并导入模块：

```nix
{
  description = "NixOS 路由器通用模块";
  outputs = { self }: {
    nixosModules.default = ./modules;
  };
}
```

在主机配置中：
```nix
{
  inputs.nixos-router.url = "github:dnfn-tech/nixos-router";
  outputs = { self, nixpkgs, nixos-router, ... }: {
    nixosConfigurations.my-router = nixpkgs.lib.nixosSystem {
      modules = [
        nixos-router.nixosModules.default
        # 未来：services.nixosRouter.webui.enable = true;
      ];
    };
  };
}
```

## 配置（Configuration）
- 单一真源：`/var/lib/nixos-router/config.json`
- 运行态：`/var/lib/nixos-router/state.db`（SQLite）
- 插件配置位于 `config.json` 的 `plugins.<id>` 命名空间
- 详情与 Schema 纲要见 `docs/backend-architecture.md` 与 `docs/plugins.md`

## 文档（Documentation）
- 需求与信息架构：[`docs/requirements.md`](docs/requirements.md)
- 后端服务架构：[`docs/backend-architecture.md`](docs/backend-architecture.md)
- 插件与页面控制：[`docs/plugins.md`](docs/plugins.md)
- 测试计划：[`docs/test-plan.md`](docs/test-plan.md)
- 目标主机说明：[`docs/target-host-notes.md`](docs/target-host-notes.md)
- 存储选型：已在开放 PR 中提供（合入后为 `docs/storage-choice.md`）

## 开发（Development）
- 当前仓库结构：`flake.nix`、`modules/`（占位/Stub）、`docs/`（主要设计文档）
- 欢迎通过 issue/PR 讨论需求与方案；设计以 `docs/` 为准并随进度迭代

## 路线图（Roadmap）
1) Schema 与只读 UI
2) 可写核心面：WAN/LAN/DHCP+静态、DNS、防火墙+端口转发、WiFi、DDNS
3) 认证与审计完善：会话/CSRF/密码策略、审计检索与导出
4) 高级服务：家长控制、QoS、UPnP、IPv6 细节打磨
5) 系统级操作：备份/恢复、日志查看、重启、回滚与自愈
6) 插件扩展：AdBlock/Traffic/Mihomo/VLAN/Tailscale/Zerotier 等按需推进

## 安全说明（Security）
- 管理界面仅在 LAN 监听；默认不开放 WAN 管理
- 关键操作写入审计日志；敏感字段仅保存在设备 `/var/lib/nixos-router/`
- 详见 `docs/requirements.md` 与 `docs/backend-architecture.md`

## 许可（License）
待定 / TBD（暂无 LICENSE 文件）
