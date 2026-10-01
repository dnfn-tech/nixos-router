# 实现现状（v1 概览）

状态：Draft PR（未合并），默认“仅生成、不热更/不重启”。以下内容基于分支 `cursor/feat-backend-m1-16f6`。

## 已实现（M1–M9）
- 配置 schema + 校验：WAN/LAN/DHCP/静态租约、DNS、WiFi（guest/isolate）、防火墙（NAT、端口转发、blockedMacs）、SSH、DDNS（cloudflare/duckdns/aliyun/custom）、QoS、Parental、IPv6
- 只读/可写 API：`GET/PUT /api/v1/config`（机密合并）、健康检查、状态、导航、插件、客户端、作业、审计
- 会话鉴权：Cookie（HttpOnly，SameSite=Lax）、登录/查看/登出、轻量限速、密码修改
- 备份/恢复：JSON 打包下载，恢复（仅保存，不 apply）
- 生成器（generate-only）：dnsmasq/nftables/hostapd 及 QoS/Parental/IPv6 注记，插件配置写入 `generated/plugins/`
- apply 作业：串行队列、生成片段、（可选）reload 钩子；默认 generate-only
- Nix 接线：flake + NixOS 模块（`applyReload`/`allowReboot` 默认 false）
- CI：GitHub Actions 运行 `backend` 下 Go 测试

## 有意延后（后续迭代）
- 守护进程编排与联动：Mihomo/Tailscale/Zerotier/VLAN 的真实服务管理（当前仅生成配置/注记）
- QoS/Parental 的实时内核/服务级执行（当前仅注记）
- 更丰富的实时状态：接口速率、信号质量、流量统计等
- flake `vendorHash` 固定：需在可用 nix 构建环境中计算

## 默认安全策略
- 默认不执行 reload/restart（`applyReload=false`），保守生成
- 重启接口默认禁用（需显式 `allowReboot`/环境变量开启）
- 仅 LAN 暴露 UI/API（设计目标）

