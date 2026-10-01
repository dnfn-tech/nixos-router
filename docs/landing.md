# 首次落地指南（NixOS 主机）

目标：在真实 NixOS 路由器（示例主机代号 gw）上让 `services.nixos-router.backend` 可用，并将生成的片段接入到系统服务（保持默认“保守生成、不写运行态”的原则）。

本页不包含任何私密信息（WAN 账号、密码、密钥等），仅给出最小接线示例。请根据你的实际网络环境（WAN/LAN 接口名、LAN 网段）替换占位符。

## 0) 先固定 vendorHash（构建前置）

推荐在有 nix 的环境执行（本 flake 已指向具备 Go ≥1.26 的 nixpkgs 通道；可选地先固定锁文件）：

```bash
# 可选但推荐：固定到具体提交
nix flake update
# 写入真实 vendorHash
./scripts/compute-vendor-hash.sh --apply
# 验证构建
nix build .#routerd
```

脚本会自动更新 `flake.nix` 的 `vendorHash`。CI 环境暂无 nix-daemon，不能替你计算该值。

## 1) 启用后端（仅生成，默认不提权）

在目标主机的 `configuration.nix`（或等效模块）中加入最小配置：

```nix
{
  services.nixos-router.backend = {
    enable = true;
    # 默认监听 :8080；建议在生产中绑定到 LAN IP（如 192.168.1.1:8080）
    address = ":8080";
    # 仅生成，不做运行态动作（更安全）
    applyReload = false;
    applyTrafficControl = false;
    # 可选：为 TCP 端口开放防火墙（简单全局开启；要仅限 LAN，请在 address 绑定 LAN IP 或自行写更细规则）
    openFirewall = false;
  };
}
```

- 启动服务后访问 `http://<你的路由器地址>:8080/`，首次“admin”账户会在设置 `NIXOS_ROUTER_ADMIN_PASSWORD` 时创建。开发/种子模式详见 `backend/README.md`。

## 2) 需要“运行态动作”时的最小提权

当你明确需要在 Apply 时尝试：
- reload 一些常见单元（`dnsmasq.service`、`nftables.service`、`hostapd.service`），或
- 执行生成的 QoS 脚本（`qos.sh`，需要 `tc` 与 CAP_NET_ADMIN）

请同时打开：

```nix
{
  services.nixos-router.backend = {
    applyReload = true;              # 允许尝试 reload（best-effort）
    applyTrafficControl = true;      # 允许尝试执行 qos.sh（需 CAP_NET_ADMIN）
    privilegedApply = true;          # 显式提权到 root（最小明确原则：默认不提权）
  };
}
```

注意：
- 即使未提权，后端仍会完成“仅生成”并返回 notes 说明哪些运行态步骤被跳过或失败（不致命）。
- `applyTrafficControl = true` 仅在 `applyReload = true` 时才会尝试执行脚本；否则只会生成。

## 3) 消费生成的片段（接线示例）

生成的片段默认存放到 `${stateDir}/generated/`（默认 `/var/lib/nixos-router/generated/`）。
最小接线（可选、保守）：

### 3.1 dnsmasq

- 推荐：让 NixOS 模块包含该片段（本模块提供轻量钩子）：

```nix
{
  services.nixos-router.backend.consumeGenerated = true;
  # 等价于在 dnsmasq 中追加：
  # services.dnsmasq.extraConfig = "conf-file=/var/lib/nixos-router/generated/dnsmasq.conf.fragment";
}
```

- 或者你也可以在自己模块中显式追加同样的 `extraConfig`。

### 3.2 nftables

片段为 `nftables.nft.fragment`。接入方式示例（按你的 nftables 管理方式选择其一）：
- 使用 `networking.nftables` 的：将片段内容（或 `include` 指令）并入你的规则文件；
- 或手动执行 `nft -f /var/lib/nixos-router/generated/nftables.nft.fragment`（需自行管理持久化）。

注意：本后端的 nftables 生成采取“最小可用骨架”，请与你的现有规则合并时留意链名/优先级。

### 3.3 hostapd

片段为 `hostapd.conf.fragment`。由于 NixOS 的 hostapd 配置多使用模块化结构（`services.hostapd`/`networking.wireless` 等），建议按需将片段内容拣选并入你的配置；或者通过外部文件 include（视你的 hostapd 管理方式而定）。

## 4) 运行校验

1. 在 UI 中保存配置（会写入 `${stateDir}/config.json`）。
2. 点击 Apply：
   - 仅生成：`appliedRuntime=false`；`generated/` 下应出现对应片段。
   - generate+reload：若成功会返回 `notes`（包括哪些单元已尝试 reload，QoS 是否执行）。
3. 查看 `generated/`：
   - `dnsmasq.conf.fragment`
   - `nftables.nft.fragment`
   - `hostapd.conf.fragment`
   - `qos.sh`（当 QoS 启用且提供速率时）
   - `plugins/*`（各插件的注记或配置）

## 5) 常见问题

- 无法读取 `/var/lib/misc/dnsmasq.leases`：
  - 该文件权限因发行版/部署而异；读取失败不会中断 API，仅影响客户端列表/近似在线状态。
- `systemctl not found`：
  - 在极简容器/环境中可能不存在 systemctl；Apply 会带上说明并继续（仅生成）。
- QoS 未生效：
  - 确认 `applyTrafficControl=true` 且 `privilegedApply=true`，并检查 `qos.sh` 是否根据你的 WAN/LAN 接口名称生成正确。

