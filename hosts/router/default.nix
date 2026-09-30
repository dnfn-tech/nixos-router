{
  imports = [
    ./hardware-configuration.nix
    ../../modules/router.nix
  ];

  nixpkgs.hostPlatform = "x86_64-linux";
  networking.hostName = "router";
  system.stateVersion = "26.05";

  # 按实际网卡名修改。首次安装后可用 `ip -br link` 确认。
  router = {
    wan.interface = "enp1s0";
    lan.interface = "enp2s0";
    lan.address = "192.168.88.1";
    lan.prefixLength = 24;
    dhcp.start = "192.168.88.100";
    dhcp.end = "192.168.88.200";
  };

  users.users.dnf = {
    isNormalUser = true;
    extraGroups = [ "wheel" ];
    openssh.authorizedKeys.keys = [
      "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAICqZJCYeC3MywT334aZt/AoJ0HnvpycYul5KXmn7wpU7 dnf@ws"
    ];
  };
}
