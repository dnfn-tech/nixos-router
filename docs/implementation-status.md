# 实现现状（v1 概览）

状态：进行中（M10），默认“仅生成、不热更/不重启”。工作基于 `main`。

## 已实现（M1–M10 进展）
- 配置 schema + 校验：WAN/LAN/DHCP/静态租约、DNS、WiFi（guest/isolate）、防火墙（NAT、端口转发、blockedMacs）、SSH、DDNS（cloudflare/duckdns/aliyun/custom）、QoS、Parental、IPv6
- 只读/可写 API：`GET/PUT /api/v1/config`（机密合并）、健康检查、状态、导航、插件、客户端、作业、审计
- 会话鉴权：Cookie（HttpOnly，SameSite=Lax）、登录/查看/登出、轻量限速、密码修改
- 备份/恢复：JSON 打包下载，恢复（仅保存，不 apply）
- 生成器（generate-only）：dnsmasq/nftables/hostapd 及 QoS/Parental/IPv6 注记，插件配置写入 `generated/plugins/`
- apply 作业：串行队列、生成片段、（可选）reload 钩子；默认 generate-only
- Nix 接线：flake + NixOS 模块（`applyReload`/`allowReboot` 默认 false）
- CI：GitHub Actions 运行 `backend` 下 Go 测试
- （M10）插件编排骨架：当 `applyReload=true` 时，针对已知插件（mihomo/tailscale/zerotier）按启用状态尝试 `systemctl try-reload-or-restart` 或 `try-stop`，缺失单元不致命、记录 notes；VLAN 仅注记
- （M10）生成的插件片段内写明可能关联的 unit 名称与 reload 策略注释
- （M10）QoS/Parental 注记加深：附安全的 `tc`/`nftables` 骨架注释（不执行）

## 有意延后（后续迭代）
- 更完整的守护进程联动与状态回传（当前为保守的 best-effort）
- QoS/Parental 的实时内核/服务级执行与回读（当前仅注记）
- 更丰富的实时状态：接口速率、信号质量、流量统计等
- flake `vendorHash` 固定：在可用 nix 构建环境中计算（见下）

## 默认安全策略
- 默认不执行 reload/restart（`applyReload=false`），保守生成
- 重启接口默认禁用（需显式 `allowReboot`/环境变量开启）
- 仅 LAN 暴露 UI/API（设计目标）

## Nix vendorHash（M10）
- CI 容器内暂不可用 nix-daemon，无法在此环境直接计算 vendorHash
- 在本地/有 nix 的环境执行：
  - `./scripts/compute-vendor-hash.sh` 打印建议哈希
  - `./scripts/compute-vendor-hash.sh --apply` 将自动更新 `flake.nix` 的 `vendorHash`
- 之后 `nix build .#routerd` 应可成功；若依然失败，请将构建输出中的 `got: sha256-...` 替换进 `flake.nix`

