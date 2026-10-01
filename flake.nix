{
  description = "NixOS 路由器通用模块 + Go 后端（内嵌 WebUI）";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-24.05";
  inputs.flake-utils.url = "github:numtide/flake-utils";

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
        lib = pkgs.lib;
        routerdPkg = pkgs.buildGoModule {
          pname = "routerd";
          version = "0.1.0";
          src = ./.;
          subPackages = [ "backend/cmd/routerd" ];
          # TODO(M10): 首次可在有 nix 的环境执行：
          #   ./scripts/compute-vendor-hash.sh --apply
          # 将下行替换为真实哈希以稳定缓存
          vendorHash = lib.fakeSha256;
        };
      in
      {
        packages = {
          default = routerdPkg;
          routerd = routerdPkg;
        };
        apps.default = {
          type = "app";
          program = "${routerdPkg}/bin/routerd";
        };
      }
    ) // {
      nixosModules.default = ./modules;
    };
}
