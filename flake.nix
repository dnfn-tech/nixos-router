{
  description = "NixOS 路由器配置管理";

  inputs = {
    # 与 nixos-home 使用同一份 NixOS 26.05 锁定版本。
    nixpkgs.url = "github:nixos/nixpkgs/f4f698677b11021a8f84f452e23ae9ef2427bec3";
  };

  outputs =
    { self, nixpkgs }:
    {
      nixosConfigurations.router = nixpkgs.lib.nixosSystem {
        modules = [ ./hosts/router ];
      };
    };
}
