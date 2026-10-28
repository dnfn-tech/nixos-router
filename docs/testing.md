# 测试（NixOS VM 多节点）

本仓库提供基于 NixOS 测试框架（QEMU）的多节点 VM 集成测试，作为 flake checks 运行。

- 节点拓扑：
  - upstream（假 ISP）：vlan1，静态 `10.0.0.1/24`，`dnsmasq` 提供 DHCP+DNS，并带测试 A 记录 `test.isp → 10.0.0.1`
  - router：导入本 flake 的 NixOS 模块，`vlan1=WAN(DHCP)`、`vlan2=LAN(192.168.1.1/24)`；测试中手工配置 `dnsmasq` 与 `networking.nat`，后端以 `dev=true` 便于登录流程
  - client：接入 `vlan2`，DHCP 获取地址与 DNS

## 运行方式

需要 x86_64 Linux 构建器支持 KVM，并允许 Nix 构建用户读写 `/dev/kvm`；此 check 声明了 `kvm` 和 `nixos-test` 构建特性，不会自动回退到软件模拟。

构建并执行 check：

```bash
nix build .#checks.x86_64-linux.vm-router -L
```

交互式调试（可在 Python 测试驱动里交互）：

```bash
nix run .#checks.x86_64-linux.vm-router.driverInteractive
```

进入后可以通过 `router.shell_interact()`、`upstream.shell_interact()`、`client.shell_interact()` 进入对应节点交互式 shell，便于排查网络与服务状态。

## 覆盖断言

测试脚本断言以下行为：

1) `nixos-router-backend.service` 正常启动，且 `:8080` 端口监听
2) `GET /api/v1/status` 200
3) `POST /api/v1/session` 登录成功（测试使用 `admin/adminadmin`）
4) client 在 LAN 上通过 DHCP 正常拿到租约（`192.168.1.0/24`）
5) 等待 router 的 WAN DHCP 地址和上游路由就绪后，client 通过 NAT 能 `ping 10.0.0.1`（上游，最多重试 120 秒）
6) client 解析 `test.isp` 得到 `10.0.0.1`
7) 通过 API 触发一次 apply 作业并轮询到 `success`（本测试以 generate-only 运行，不做运行态 reload）

## CI

GitHub Actions 使用 `cachix/install-nix-action` 运行 `vm-router`、`vm-managed-router`、`vm-managed-wifi` 三个 check，并评估两个模块契约。运行前检查并开放 `/dev/kvm`，因为旧 `vm-router` 仍要求 KVM。宿主没有 KVM 设备或设备不可读写时直接失败，需要改用支持 KVM 的 runner。

## 保存与应用反馈的浏览器回归

`tests/web-save-apply.cjs` 使用 Node 内置 test runner、`playwright-core` 与已安装 Chromium。
它会启动仅监听 loopback 的临时后端，使用独立 SQLite/config 目录并在结束时清理，不操作现有部署。
设置 `CHROMIUM_PATH` 为 Chromium 可执行路径，`NODE_PATH` 指向包含 `playwright-core` 的 node_modules：

```bash
GOCACHE=/tmp/router-go-cache go -C backend build -buildvcs=false -o /tmp/routerd-web-test ./cmd/routerd
ROUTERD_TEST_BINARY=/tmp/routerd-web-test node --test tests/web-save-apply.cjs
```

保存、重新读取以及跨页放弃修改通过真实 API 验证；应用故障和警告用受控 HTTP 响应注入，证明页面反馈，不代替真实服务/网络验收。

## 网络管理器互斥

```bash
nix eval --impure --json --file tests/module-apply.nix
GOCACHE=/tmp/router-go-cache go -C backend test ./internal/server -run TestApplyNetworkManager
```

Nix 断言在 `applyReload=true` 时拒绝同时启用 NetworkManager、systemd-networkd、dhcpcd 或 ConnMan。
预览/仅生成模式可与这些管理器并存。Apply 前检查通过 CommandRunner 隔离系统调用，验证冲突时不覆盖生成文件、不执行修改命令。
这证明配置与应用边界，不证明完整网络接管、运行期间其他进程不会启动或三节点转发已经实现。

## Web 全局布局与数据契约回归

同一 `tests/web-save-apply.cjs` 现覆盖 23 项回归：24 个页面/面板在四种宽度下的溢出和标签检查，真实配置读写、WAN/DDNS 条件字段、WiFi 与插件密钥、备份恢复、密码和客户端编辑，以及注入的错误/慢响应/异步作业。

测试中的应用失败、管理器冲突和异步作业响应是受控注入；字段保存与账户、备份、设备编辑使用真实临时后端。截图输出到 `/tmp/nixos-router-web-audit/after/`。这些截图和测试不替代真实设备转发或无线验证。

完整核查结论见 [Web 全局核查结果](superpowers/specs/2026-10-28-web-global-audit.md)。

## managed 网络候选的隔离验收

generate-only 的页面/容器检查不能作为网关网络验收。新的 managed 检查由 routerd 创建实际网桥、地址、DHCP/DNS 和 nft 规则，LAN 客户端通过真实 API 应用配置；测试驱动另有隔离控制通道。

```bash
# 在仓库根执行；path: 包含尚未提交的新测试文件。
nix eval --impure --json --file tests/module-managed.nix
nix eval --impure --json --file tests/module-apply.nix
nix build --keep-going "path:$PWD#checks.x86_64-linux.vm-managed-router" \
  "path:$PWD#checks.x86_64-linux.vm-managed-wifi" -L

# 后端全部逻辑与竞态检查。
(cd backend && go test -race ./...)
```

两个新 VM 检查允许 QEMU TCG，因此无 `/dev/kvm` 时仍能运行，但会更慢。无线使用同一内核的 mac80211_hwsim 两个无线电和独立 station 网络命名空间，验证 WPA2、真实 DHCP 租约、禁用/重启，不声称验证 MT7927。

浏览器回归：`tests/web-save-apply.cjs` 与 `tests/web-managed-capabilities.cjs`；需提供 Playwright NODE_PATH、CHROMIUM_PATH 和重新构建的 ROUTERD_TEST_BINARY。能力提示测试使用拦截响应，仅作为 UI 证据。

实体 gw 的授权、迁移影响和验收顺序见 [硬件验收方案](gw-hardware-acceptance.md)。具体结果、制品与未验证范围以 [候选验收报告](gw-readiness.md) 为准。
