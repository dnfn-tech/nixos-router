# 实现现状（v1 概览）

状态：进行中（M11），默认“仅生成、不热更/不重启”。工作基于 `main`。

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

## 有意延后（后续迭代）
- 更完整的守护进程联动与状态回传（当前为保守的 best-effort）
- QoS/Parental 的实时内核/服务级执行与回读（当前仅注记/可选执行脚本）
- 更丰富的实时状态：接口速率、信号质量、流量统计等
- flake `vendorHash` 固定：在可用 nix 构建环境中计算（见下）

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

