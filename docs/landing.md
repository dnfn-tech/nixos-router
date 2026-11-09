# 通用模块接入指南（NixOS 主机）

本仓库提供基础框架；具体接口、地址、无线能力和旧网络迁移由集成仓库决定。以下模板不代表任何实体设备已部署或验收。

## 选择运行模式

- **预览/仅生成**：默认模式，可与现有网络管理器并存。保存与生成成功不表示网络已经应用。旧的片段消费模式见本文后半部分。
- **managed**：routerd 独占明确授权的接口，管理单 LAN 网桥及自己的 DHCP/DNS/AP/nft 服务。此模式与 NetworkManager、networkd、dhcpcd、ConnMan，以及宿主 Kea/dnsmasq/hostapd/防火墙服务互斥。

## managed 最小模块模板

在集成 flake 中固定 `nixos-router` 的提交与 lock，导入 `inputs.nixos-router.nixosModules.default`。在主机模块中配置：

```nix
{ inputs, pkgs, ... }: {
  services.nixos-router.backend = {
    enable = true;
    package = inputs.nixos-router.packages.${pkgs.stdenv.hostPlatform.system}.routerd;
    address = "[::]:8080";
    managed = {
      enable = true;
      interfaces = [ "eth0" "eth1" ]; # 有线 WAN/LAN 的显式授权集合，不包含网桥
      wifiInterface = ""; # 有 AP 时填写独立无线接口，并清除宿主对它的地址/DHCP配置
      countryCode = "CN"; # 由集成方选择实际法规区域
      bootstrapConfigFile = "/run/router-secrets/bootstrap.json";
      adminPasswordFile = "/run/router-secrets/admin-password";
    };
  };
  networking.useDHCP = false;
  networking.dhcpcd.enable = false;
  networking.networkmanager.enable = false;
  networking.useNetworkd = false;
  systemd.network.enable = false;
  services.connman.enable = false;
  services.kea.dhcp4.enable = false;
  services.dnsmasq.enable = false;
  services.hostapd.enable = false;
  networking.firewall.enable = false;
  networking.nat.enable = false;
  networking.nftables.enable = false;
}
```

集成方还需删除受管接口的宿主静态 IPv4/DHCP 声明及旧桥定义，处理旧单元。模板中的普通赋值不会强制覆盖其他模块的冲突定义：应在集成源头明确唯一所有者。不要全局打开 API 防火墙端口；managed 自有规则限制 API 和 NixOS SSH 端口从 LAN 访问。

两个文件均是**运行时文件**，由集成方的密钥机制在服务启动前提供，权限限制为 root 可读；不要把真实密码写进 Nix 字符串或 `writeText`，它们会进入 Nix store。初始管理员密码至少8字节。已有账户时不会用此文件重置密码。

`bootstrap.json` 使用本仓库配置 schema。首个有线测试配置可从以下结构开始，按现场修改；不要直接用示例地址切换现网：

```json
{
  "system": {"hostname": "router", "timezone": "UTC"},
  "wan": {"interface": "eth0", "mode": "dhcp"},
  "lan": {"bridgeName": "br-lan", "ipv4Cidr": "192.168.50.1/24", "ports": ["eth1"], "dhcp": {"enable": true, "rangeStart": "192.168.50.100", "rangeEnd": "192.168.50.150", "leaseMins": 60}},
  "dns": {"enableDnsmasq": true, "upstreams": [], "domain": "lan"},
  "wifi": {"enable": false, "bridgeToLan": true, "aps": []},
  "firewall": {"enable": true, "natEnabled": true}
}
```

启用 WiFi 时提供独立 `wifiInterface`、一个启用的 WPA2 AP、合法国家/非DFS信道和运行时密钥。当前配置不支持 WPA3、WiFi 6/7 参数、访客/多AP、IPv6路由、PPPoE等高级能力；不要把宿主已有能力视为被框架自动保留。

## 生命周期与验证边界

- 首次启动仅在 `config.json` 缺失时复制 bootstrap；后续编辑保存为草稿，必须显式 Apply。
- 启动恢复 last-good，不自动应用尚未提交的草稿。损坏草稿可回退到有效 last-good 提供管理入口；last-good 损坏时拒绝接管。
- Apply 返回作业编号后才变更网络。以终态作业及客户端验证判定结果，不能以 HTTP 202 或 WAN 等待租约状态判定联网完成。
- 普通应用失败尝试恢复 last-good；没有 LAN 改址超时自动回滚。已有 last-good 后桥名和 WAN 物理接口迁移被拒绝。
- 未标记归属的已有桥不会被自动收编；专属协议单元由 routerd 控制。停止或移除模块不是自动恢复旧网络配置的迁移操作。
- SSH/系统身份由 NixOS 配置；后台不会以页面草稿修改 sshd、主机名和时区。
- 合入前运行 [模块、浏览器及 VM 检查](testing.md)。目标系统的整机闭包、控制台、迁移与硬件验收属于集成方责任。

## 旧模式：生成片段与可选消费

以下保留原有生成模式接线说明，**不要与 managed 模式同时启用**。

目标：在真实 NixOS 路由器（示例主机代号 gw）上让 `services.nixos-router.backend` 可用，并将生成的片段接入到系统服务（保持默认“保守生成、不写运行态”的原则）。

本页不包含任何私密信息（WAN 账号、密码、密钥等），仅给出最小接线示例。请根据你的实际网络环境（WAN/LAN 接口名、LAN 网段）替换占位符。

## 0) 构建前置（已固定 vendorHash）

- 已固定：`vendorHash = "sha256-MM1ODEBButuG1Yalmyxv1mkJmc4Va4tclJpq1q0IAcc="`，且 flake.lock 已锁定到 `nixpkgs-unstable`。
- 私有 worker 已验证：`nix build .#routerd` 成功。

```bash
nix build .#routerd
```

如后续依赖更新需重算 vendor：在 Nix 主机上执行 `./scripts/compute-vendor-hash.sh --apply` 后再运行 `nix build .#routerd`。

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
   - generate+reload+consume（启用 `consumeGenerated` 且 `privilegedApply` 时）：在 reload 前会以 `nft -f` 直接应用 `nftables` 片段；成功后将把 `generated/` 快照为 last-good（见下）
3. 查看 `generated/`：
   - `dnsmasq.conf.fragment`
   - `nftables.nft.fragment`
   - `hostapd.conf.fragment`
   - `qos.sh`（当 QoS 启用且提供速率时）
   - `plugins/*`（各插件的注记或配置）
4. 成功后快照（仅 generate+reload 成功时）：
   - `${stateDir}/revisions/<rev>/`：保存当次生成的完整片段集
   - `${stateDir}/last-good.rev`：文本指针，指向最近一次成功的 `<rev>`
   - `${stateDir}/last-good.json`：对应的完整 `config.json` 备份

## 5) 常见问题

- 无法读取 `/var/lib/misc/dnsmasq.leases`：
  - 该文件权限因发行版/部署而异；读取失败不会中断 API，仅影响客户端列表/近似在线状态。
- `systemctl not found`：
  - 在极简容器/环境中可能不存在 systemctl；Apply 会带上说明并继续（仅生成）。
- QoS 未生效：
  - 确认 `applyTrafficControl=true` 且 `privilegedApply=true`，并检查 `qos.sh` 是否根据你的 WAN/LAN 接口名称生成正确。
- 失败与回滚：
  - 当 `consumeGenerated` 或 reload 任一步失败时，后端将尝试把 `generated/` 回滚为 `${stateDir}/revisions/$(cat last-good.rev)/`，并在 `consumeGenerated` 与 `privilegedApply` 均开启时以 `nft -f` 重新加载 last-good；随后 best-effort reload 核心单元。失败原因与回滚说明会在作业 `notes` 中体现。


- 直接消费 nft 使用 `routerd_filter` / `routerd_tmp` / `routerd_nat` 专属表并原子替换，跳过 nftables 单元 reload。旧实验版本的通用表及快照需按 backend/README.md 的升级说明人工迁移。
