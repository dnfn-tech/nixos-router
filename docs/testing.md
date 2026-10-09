# 测试（NixOS VM 多节点）

本仓库提供基于 NixOS 测试框架（QEMU）的多节点 VM 集成测试，作为 flake checks 运行。

- 节点拓扑：
  - upstream（假 ISP）：vlan1，静态 `10.0.0.1/24`，`dnsmasq` 提供 DHCP+DNS，并带测试 A 记录 `test.isp → 10.0.0.1`
  - router：导入本 flake 的 NixOS 模块，`vlan1=WAN(DHCP)`、`vlan2=LAN(192.168.1.1/24)`；测试中手工配置 `dnsmasq` 与 `networking.nat`，后端以 `dev=true` 便于登录流程
  - client：接入 `vlan2`，DHCP 获取地址与 DNS

## 运行方式

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
5) client 通过 NAT 能 `ping 10.0.0.1`（上游）
6) client 解析 `test.isp` 得到 `10.0.0.1`
7) 通过 API 触发一次 apply 作业并轮询到 `success`（本测试以 generate-only 运行，不做运行态 reload）

## CI

GitHub Actions 使用 `cachix/install-nix-action` 运行该 check；若宿主支持 KVM，会尝试开放 `/dev/kvm` 以加速虚拟化（不可用时自动回退纯 QEMU）。

