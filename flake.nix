{
  description = "NixOS 路由器通用模块 + Go 后端（只读 API）";

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
          # 初次构建时会提示正确的 vendorHash；替换后可复用缓存
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
