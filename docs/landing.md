# 通用模块接入指南（NixOS 主机）

本仓库提供基础框架；具体接口、地址、无线能力和旧网络迁移由集成仓库决定。以下模板不代表任何实体设备已部署或验收。

## 选择运行模式

- **预览/仅生成**：默认模式，可与现有网络管理器并存。保存与生成成功不表示网络已经应用。旧的片段消费模式见本文后半部分。
- **managed**：routerd 独占明确授权的接口，管理一个有线 LAN 网桥、可选独立无线网段及自己的 DHCP/DNS/AP/nft 服务。此模式与 NetworkManager、networkd、dhcpcd、ConnMan，以及宿主 Kea/dnsmasq/hostapd/radvd/防火墙服务互斥。

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
  services.kea.dhcp6.enable = false;
  services.dnsmasq.enable = false;
  services.hostapd.enable = false;
  services.radvd.enable = false;
  networking.firewall.enable = false;
  networking.nat.enable = false;
  networking.nftables.enable = false;
}
```

集成方还需删除受管接口的宿主静态 IPv4/IPv6 地址、DHCP 声明及旧桥定义，处理旧单元。模板中的普通赋值不会强制覆盖其他模块的冲突定义：应在集成源头明确唯一所有者。不要全局打开 API 防火墙端口；managed 自有规则限制 API 和 NixOS SSH 端口从 LAN 访问。

### 宿主 UDP 服务的 WAN 入口

需要保留 ZeroTier 等宿主服务入口时，由集成方显式设置：

```nix
services.nixos-router.backend.managed.wanUDPPorts = [ 9993 ];
```

默认 `[]`，最多 64 项，每项为 1–65535。规则只对实际 WAN 出口放行指定 UDP 端口，覆盖 IPv4/IPv6；PPPoE 使用逻辑出口，不额外打开物理口。该权限来自 NixOS 配置，Web 草稿不能增加入口；服务本身、overlay 路由及其管理权限仍由集成方分别配置，不会自动获得家庭 LAN 身份。

移除端口并重启执行器以加载部署策略后，新入站流量不再放行；已建立连接仍遵循连接跟踪。该配置不开放 TCP 管理端口或新增转发权限，不能用 `networking.firewall.allowedUDPPorts` 代替受管规则。实际重启可能短暂中断网络，应纳入已审核的迁移步骤。

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

启用 WiFi 时提供独立 `wifiInterface`、一个启用的 AP、合法国家/非 DFS 信道和运行时密钥。支持 WPA2-PSK 或纯 WPA3-SAE（强制 PMF）；无线可桥接到有线 LAN，也可使用独立 IPv4 网段，并显式选择无线管理及跨网段访问权限。配置 `standard`（`wifi4`／`wifi5`／`wifi6`／`wifi7`）和 `channelWidthMHz`（20／40／80）时，执行器在变更网络前查询实际 PHY 的 AP 模式、频段、标准及全部占用信道，缺少能力或受法规限制时明确拒绝，不静默降级。空标准、默认带宽保留旧配置行为。多 AP、访客配置、DFS、6 GHz、MLO 仍不属于当前候选能力。

IPv6 支持 Ethernet SLAAC／DHCPv6、动态 DNS 和委派前缀生命周期；独立无线网段与有线 LAN 可各分配一个 /64，委派容量不足时不会重复使用前缀。PPPoE6 已通过真实 IPv6CP、DHCPv6/PD、双栈流量及完整生命周期软件验收；这不证明实体设备或真实运营商已验收。软件验证、设备驱动及射频验收的具体证据和边界见 [gw 集成对照表](gw-integration-matrix.md)。

PPPoE 使用独立的 `nixos-router-pppoe` 服务和逻辑出口 `nr-ppp0`，物理 WAN 仍须在接口 allowlist 内。凭据从运行时配置写入 root 私有 options 文件，不应写入 Nix store。应用成功或服务 active 不代表认证及联网成功；`awaiting-pppoe-session` 表示尚未观察到 PPP 地址。完整验收进度见 [测试说明](testing.md)。

## managed 权限与升级布局

managed 使用 `nixos-router-backend`（静态用户 `nixos-router-api`，无 capabilities）和独立 root `nixos-router-executor`。后者仅监听受保护 Unix socket，通过内核 UID 认证，只接收配置级请求。执行器恢复 last-good 后报告就绪，API 再连接；不再为 managed API 开启旧的 `applyReload` / `privilegedApply` 开关。当前不提供重启 RPC，`allowReboot` 必须保持关闭。

- `backend.stateDir`（默认 `/var/lib/nixos-router`）继续保存 root 私有运行状态、last-good 和执行器作业日志；原 generations 内的绝对路径不移动。
- `managed.apiStateDir` 默认 `${stateDir}-api`，保存 API 账户/会话数据库、草稿和 API 作业。两目录不能互相包含；其父目录须预先存在且由 root 保护。
- `nixos-router-state-init` 首次离线复制旧账户数据库及 SQLite WAL/恢复文件、草稿，源数据保留。检查旧 daemon/executor 锁、文件归属和权限，拒绝 symlink、hardlink、未知既有目标和不可信配置。迁移成功后不再用旧副本覆盖 API 数据；已发布目录丢失会拒绝自动复活旧账户。
- bootstrap 和初始密码通过 systemd `LoadCredential` 传递，不需要将 root 密钥文件改为 API 可读。自定义 `package` 必须同时提供 `routerd` 和 `router-executor`。
- API 重启不触发网络恢复，也不取消执行器已接受的作业；执行器重启从 last-good 恢复。按原编号查询结果；执行器未找到编号时显示 unknown，不自动重放。租约读取失败明确不可用，不能视为空客户端列表。

升级旧 managed 服务时必须让旧 API 停止后再初始化；若锁仍被占用，启动会明确失败。不要手工递归 chown 原 root 运行目录，也不要用旧 API 数据覆盖已迁移目录。普通重启 API 使用 `systemctl restart nixos-router-backend`；需要恢复运行配置时重启 `nixos-router-executor`，这可能中断网络，应在集成维护窗口处理。

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


## 配置版本与并发写入

保存的 JSON 使用 `schemaVersion: 1` 和不透明的 `revision`。旧的无版本草稿首次加载时先以原始字节写入 `config.json.v0.bak`（0600），再原子迁移；备份冲突、未知字段、重复键和未来版本会拒绝迁移，保留原文件。last-good 等运行快照只读加载，不因观察而迁移。

API 调用者先 `GET /api/v1/config`，保留响应的完整 `ETag`，再将它作为写请求的 `If-Match`。`PUT /config`、插件配置、设备 PATCH 和备份恢复共享同一个修订；成功返回新 ETag。缺失条件返回 428/config_revision_required，过期条件返回 409/config_conflict。不得收到 409 后自动取新修订并重发旧内容；应重新加载、核对再保存。

旧 JSON/备份格式继续可导入，但无条件写 API 不再接受。这是为了阻止旧调用者静默覆盖新配置；集成脚本须同步增加 GET/If-Match。Web 自动携带编辑开始时的修订，冲突时保留输入。恢复备份也生成新修订，不重用备份中的旧身份。managed apply 作业记录其捕获的 configRevision。

### API 失败反馈

HTTP 失败响应提供 `code`、`message`、`fieldErrors`（无字段错误时为 `{}`）。字段路径使用配置 JSON 名称和零起始数组下标，例如 `lan.staticLeases.1.ip`。旧 `error`/`errors` 字段在兼容期保留；调用者应使用 `code` 判断失败类型、`message` 展示说明。保存失败不改变草稿修订；Web 保留输入并标记当前页面可见的错误字段。

API 写请求体上限为 16 MiB，JSON 和 multipart 请求均适用；超出返回 413。认证和 CSRF 检查在读取请求体之前执行。

### 密钥保留与显式清空

默认配置读取会脱敏核心凭据和插件中的密钥字段。插件使用绑定当前修订的不透明引用，编辑或重排数组时应保留对应引用；引用不是凭据，也不能跨修订或放进普通字段。需要导出实际秘密时必须显式使用受认证的非脱敏读取或备份，并按秘密文件处理。

清空使用 `PUT /api/v1/config` 的独立请求元数据（仍须 `If-Match`）：

```json
{"config": {"...": "完整配置"}, "clearSecrets": [["plugins", "custom", "config", "peers", "0", "token"]]}
```

路径由字符串段组成，插件名称或键含点、斜杠时不拆分。清空命令不写进配置或备份；整批操作通过校验后才保存，活动功能的必填凭据不能清空。Web 密码字段和高级 JSON 均提供清空选项，输入新值可取消普通密码控件的清空选择。

空串、`****` 不表示清空既有密钥。插件数组只在非秘密字段能唯一匹配原项目时保留空值对应的旧密钥；改名、重复项目等导致归属不明时拒绝，需保留引用或填写新值。已清空项目可继续保存。数组项目的删除仍表示删除整个项目。

### WiFi 认证模式

接入点 `security` 支持 `wpa2-psk` 与纯 `wpa3-sae`；省略或空值保留旧 WPA2 行为。WPA3-SAE 强制 PMF，不自动回退 WPA2。密码继续通过 `psk` 提供，受管模式要求 8–63 位可打印 ASCII；运行配置保存在 root 私有目录，API 沿用秘密脱敏与版本绑定。真实秘密须由集成方通过私有 bootstrap/秘密文件提供，不得用 `builtins.toFile`/`writeText` 将生产密码写入 Nix store。模拟无线测试不证明真实射频或设备能力。
