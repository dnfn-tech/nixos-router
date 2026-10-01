{
  description = "NixOS 路由器通用模块 + Go 后端（内嵌 WebUI）";

  # M14: 升级到能提供 Go ≥ 1.26 的 nixpkgs（优先稳定，不满足则使用 unstable）
  # 24.05 的 Go 为 1.22.x，会阻塞本仓库（go.mod 要求 ≥1.26）
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
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
          # 指定 Go 模块根，便于 vendorHash 计算与后续 vendor 固定
          modRoot = "./backend";
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
