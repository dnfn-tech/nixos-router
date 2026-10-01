# 实现现状（v1 概览）

状态：进行中（M12），默认“仅生成、不热更/不重启”。工作基于 `main`。

## 已实现（M1–M11 进展）
- 配置 schema + 校验：WAN/LAN/DHCP/静态租约、DNS、WiFi（guest/isolate）、防火墙（NAT、端口转发、blockedMacs）、SSH、DDNS（cloudflare/duckdns/aliyun/custom）、QoS、Parental、IPv6
- 只读/可写 API：`GET/PUT /api/v1/config`（机密合并）、健康检查、状态、导航、插件、客户端、作业、审计
- 会话鉴权：Cookie（HttpOnly，SameSite=Lax）、登录/查看/登出、轻量限速、密码修改
- 备份/恢复：JSON 打包下载，恢复（仅保存，不 apply）
- 生成器（generate-only）：dnsmasq/nftables/hostapd 及 IPv6 注记，插件配置写入 `generated/plugins/`
- apply 作业：串行队列、生成片段、（可选）reload 钩子；默认 generate-only
- Nix 接线：flake + NixOS 模块（`applyReload`/`allowReboot` 默认 false）
- CI：GitHub Actions 运行 `backend` 下 Go 测试
- （M10）插件编排骨架：当 `applyReload=true` 时，针对已知插件（mihomo/tailscale/zerotier）按启用状态尝试 `systemctl try-reload-or-restart` 或 `try-stop`，缺失单元不致命、记录 notes；VLAN 仅注记
- （M10）生成的插件片段内写明可能关联的 unit 名称与 reload 策略注释
- （M11）状态增强：`GET /api/v1/status` 增加接口实时信息（基于 `/sys/class/net` 的链路 up/down、可读速率/双工、RX/TX 字节计数与基于内存样本的速率估算）、`sources` 源标注（sysfs/stub）、WiFi 源为 stub；`/api/v1/clients` 增补 `lastSeen`（来自租约到期的近似）
- （M11）生成器增强（仍默认不执行）：
  - Parental：当 `parental.enable=true` 时，生成 `generated/parental.nft.fragment`，包含 `set blocked_macs` 与 `chain input` 引用（时间表 schedule 以注释保真，未强制执行）
  - QoS：当 `qos.enable=true` 时，生成可复用脚本 `generated/qos.sh`（HTB 上行整形 + ingress 简化限速），并保留 `qos.conf.fragment` 注记；新增运行态开关 `applyTrafficControl`（默认 false，且需 `applyReload=true` 才可能执行）
- （M11）前端：总览页展示新实时字段（速率/双工、累计与速率估算），缺省优雅降级；同步 `web/` 与 `backend/web/` 的嵌入资源
  
## 新增（M12）
- NixOS 模块首个落地就绪：
  - 新增 `applyTrafficControl`（默认 false）接线到后端 CLI/env
  - 新增 `privilegedApply`（默认 false）：仅在需要运行态动作时显式提权到 root；默认保持 `DynamicUser` 沙盒
  - 可选 `openFirewall`（默认 false）：为 HTTP 端口打开 TCP（简单全局开启；更细的 LAN 约束请由操作者在防火墙中实现或将 `address` 绑定到 LAN IP）
  - 可选 `consumeGenerated`（默认 false）：当启用时，为 `dnsmasq` 追加包含 `generated/dnsmasq.conf.fragment` 的 `extraConfig`（保守默认关闭，避免无意更改现网）
- 后端参数对齐：支持 `--apply-traffic-control` 与 `NIXOS_ROUTER_APPLY_TRAFFIC_CONTROL`
- 文档：新增 `docs/landing.md`（首次落地指南）

## 有意延后（后续迭代）
- 更完整的守护进程联动与状态回传（当前为保守的 best-effort）
- QoS/Parental 的实时内核/服务级执行与回读（当前仅注记/可选执行脚本）
- 更丰富的实时状态：接口速率、信号质量、流量统计等
- flake `vendorHash` 固定：在可用 nix 构建环境中计算（见下）

## 新增（M13）
- 后端状态增强：`GET /api/v1/status` 增加守护进程/插件 unit 状态（best-effort）：
  - 核心：`dnsmasq.service`、`hostapd.service`、`nftables.service`
  - 插件：`mihomo.service`、`tailscaled.service`、`headscale.service`（当 Tailscale 为 headscale 模式）与 `zerotier-one.service`
  - 当存在 `systemctl` 时，逐个查询 `is-active` 并映射为 `{active|inactive|failed|unknown|missing}`；当缺失 `systemctl` 时，来源标记为 `stub`，状态优雅降级为 `unknown`
  - `sources.units` 增补来源标记：`systemctl|stub`
- 前端总览：新增“守护进程/插件状态”卡片，按常见顺序展示，缺省优雅降级
- 测试：新增针对 systemctl 缺失与状态映射的单测，覆盖核心与插件单元
- flake 细化：为 `buildGoModule` 指定 `modRoot = \"./backend\"`（仅路径修正，便于后续 vendor 计算）

## 默认安全策略
- 默认不执行 reload/restart（`applyReload=false`），保守生成
- 默认不执行 `tc`（`applyTrafficControl=false`），即使生成了 `qos.sh`
- 重启接口默认禁用（需显式 `allowReboot`/环境变量开启）
- 仅 LAN 暴露 UI/API（设计目标）

## Nix vendorHash（M10/M11）
- CI 容器内暂不可用 nix-daemon，无法在此环境直接计算 vendorHash
- 在本地/有 nix 的环境执行：
  - `./scripts/compute-vendor-hash.sh` 打印建议哈希
  - `./scripts/compute-vendor-hash.sh --apply` 将自动更新 `flake.nix` 的 `vendorHash`
- 之后 `nix build .#routerd` 应可成功；若依然失败，请将构建输出中的 `got: sha256-...` 替换进 `flake.nix`

## Nix vendorHash（M13 进展）
- 当前 `nixpkgs-24.05` 的 Go 工具链为 1.22.x，而本仓库需要 Go ≥1.26（`backend/go.mod`）：
  - 在该环境下 `scripts/compute-vendor-hash.sh` 会于编译前失败，无法输出有效 `vendorHash`
  - 本迭代不提升 nixpkgs 固定，也不“猜测”哈希：`flake.nix` 仍保持 `vendorHash = lib.fakeSha256`
- 建议在具备 Go ≥1.26 的 Nix 环境（或较新的 nixpkgs）中执行：
  - `./scripts/compute-vendor-hash.sh --apply`
- 之后提交独立 PR 锁定 vendor 缓存

