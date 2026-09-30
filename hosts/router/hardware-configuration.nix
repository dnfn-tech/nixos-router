# 在路由器上生成后替换本文件：
#   sudo nixos-generate-config --root /mnt
# 然后把 /mnt/etc/nixos/hardware-configuration.nix 的内容提交到这里。
{ lib, ... }:
{
  assertions = [
    {
      assertion = false;
      message = "请用路由器上 nixos-generate-config 生成的 hardware-configuration.nix 替换 hosts/router/hardware-configuration.nix。";
    }
  ];

  # 占位，避免在替换硬件文件之前模块求值缺少文件系统定义。
  fileSystems."/" = lib.mkDefault {
    device = "/dev/disk/by-label/nixos";
    fsType = "ext4";
  };
  boot.loader.grub.enable = lib.mkDefault true;
  boot.loader.grub.device = lib.mkDefault "nodev";
}
