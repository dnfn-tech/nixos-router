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
          subPackages = [ "cmd/routerd" ];
          # 已在有 Nix 的主机上验证：nix build .#routerd 成功
          vendorHash = "sha256-MM1ODEBButuG1Yalmyxv1mkJmc4Va4tclJpq1q0IAcc=";
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
