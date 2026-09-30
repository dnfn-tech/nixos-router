# nixos-router

基于 NixOS 的路由器配置管理仓库。日常在这台管理机上改配置，再部署到路由器。

Nixpkgs 与 `nixos-home` 锁定在同一份 NixOS 26.05 修订。

## 目录

- `flake.nix`：入口，输出 `nixosConfigurations.router`
- `hosts/router/default.nix`：这台路由器的主机名、网卡、地址池和管理员
- `hosts/router/hardware-configuration.nix`：目标机器硬件，安装时替换
- `modules/router.nix`：NAT、防火墙、DHCP、DNS、SSH

## 部署前

1. 把 `hosts/router/default.nix` 里的 `wan.interface` 和 `lan.interface` 改成路由器上的真实网卡名。
2. 在安装环境里生成硬件配置，覆盖 `hosts/router/hardware-configuration.nix`。占位文件会故意让系统构建失败，避免把示例磁盘布局装到机器上。
3. 确认 `users.users.dnf` 的公钥仍是要用来登录路由器的钥匙。

## 部署

路由器装好 NixOS 并配好 SSH 后：

```bash
nixos-rebuild switch --flake .#router --target-host dnf@192.168.88.1 --sudo
```

只在本机检查配置是否能求值：

```bash
nix eval .#nixosConfigurations.router.config.networking.hostName
```

完整构建要等硬件文件替换之后：

```bash
nix build .#nixosConfigurations.router.config.system.build.toplevel
```

## 默认网络

| 项目 | 值 |
| --- | --- |
| WAN | DHCP |
| LAN | `192.168.88.1/24` |
| DHCP | `192.168.88.100`–`192.168.88.200` |
| DNS | `223.5.5.5`、`119.29.29.29` |
| SSH | 只对 LAN 开放，仅公钥登录 |

WAN 入站保持关闭。内网只放行 SSH、DNS 和 DHCP。
