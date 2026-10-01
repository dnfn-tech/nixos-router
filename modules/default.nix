{ config, pkgs, lib, ... }:
let
  cfg = config.services.nixos-router.backend;
  routerdPkg =
    if cfg.package != null then cfg.package
    else pkgs.buildGoModule {
      pname = "routerd";
      version = "0.1.0";
      src = ../.;
      subPackages = [ "backend/cmd/routerd" ];
      vendorHash = lib.fakeSha256;
    };
in
{
  options.services.nixos-router.backend = {
    enable = lib.mkEnableOption "nixos-router Go 后端 API (只读)";

    package = lib.mkOption {
      type = lib.types.nullOr lib.types.package;
      default = null;
      description = "routerd 包；默认从本仓库构建";
    };

    stateDir = lib.mkOption {
      type = lib.types.path;
      default = "/var/lib/nixos-router";
      description = "状态目录（config.json / state.db）";
    };

    address = lib.mkOption {
      type = lib.types.str;
      default = ":8080";
      description = "监听地址（host:port）";
    };

    dev = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = "开发模式（免鉴权 + CORS *)";
    };

    environment = lib.mkOption {
      type = lib.types.attrsOf lib.types.str;
      default = { };
      description = "额外环境变量";
    };
  };

  config = lib.mkIf cfg.enable {
    systemd.services.nixos-router-backend = {
      description = "NixOS Router Backend API";
      wantedBy = [ "multi-user.target" ];
      after = [ "network-online.target" ];
      serviceConfig = {
        ExecStart = ''
          ${routerdPkg}/bin/routerd \
            --state-dir ${cfg.stateDir} \
            --addr ${cfg.address} ${lib.optionalString cfg.dev " --dev"}
        '';
        DynamicUser = true;
        StateDirectory = "nixos-router";
        Restart = "on-failure";
        RestartSec = 2;
        AmbientCapabilities = [ ];
      };
      environment = cfg.environment // {
        NIXOS_ROUTER_STATE_DIR = cfg.stateDir;
        NIXOS_ROUTER_ADDR = cfg.address;
        NIXOS_ROUTER_DEV = lib.boolToString cfg.dev;
      };
    };
  };
}
