{ config, pkgs, lib, ... }:
let
  cfg = config.services.nixos-router.backend;
  routerdPkg =
    if cfg.package != null then cfg.package
    else pkgs.buildGoModule {
      pname = "routerd";
      version = "0.1.0";
      src = ../.;
      modRoot = "./backend";
      subPackages = [ "cmd/routerd" ];
      vendorHash = "sha256-MM1ODEBButuG1Yalmyxv1mkJmc4Va4tclJpq1q0IAcc=";
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

    applyReload = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = ''
        是否在 apply 时尝试执行运行态 reload（best-effort，可能失败不致命）。
        注意：要真正执行到 systemctl reload/try-restart 等动作，建议结合
        `privilegedApply = true;` 以 root 账户运行本服务；默认为安全考虑不提升权限，
        开启本开关但不提权时，后端会记录失败并继续完成“仅生成”。
      '';
    };

    applyTrafficControl = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = ''
        是否允许在 apply 时尝试应用 QoS 生成脚本（qos.sh，需要 CAP_NET_ADMIN）。
        仅当 `applyReload = true` 时才会尝试执行；默认关闭，仅生成片段。
        如需真正执行 tc，请同时设置 `privilegedApply = true;`（root 运行）。
        也可通过环境变量 `NIXOS_ROUTER_APPLY_TRAFFIC_CONTROL=1` 控制。
      '';
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

    allowReboot = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = "允许 /api/v1/system/reboot 执行系统重启（默认关闭，极其谨慎）";
    };

    privilegedApply = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = ''
        以 root 运行后端（仅当需要“运行态动作”时建议开启）。
        - 默认（false）：DynamicUser 沙盒用户，仅做生成与读状态（更安全）。
        - 设为 true：使用 root（或等效权限）运行，以便执行 systemctl 与 tc。
        注意：请仅在受信任的本机环境中启用。
      '';
    };

    openFirewall = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = ''
        为后端 HTTP 端口打开防火墙（TCP）。当前实现为简单开放该端口，
        并未自动限制到 LAN 接口；若需要仅 LAN 访问，请配合在 `address`
        中绑定 LAN IP（例如 192.168.1.1:8080）或自行在防火墙模块中细化接口限制。
      '';
    };

    consumeGenerated = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = ''
        由模块轻量接线消费部分生成片段（保守默认关闭）：
        - dnsmasq：通过 extraConfig 包含 “${cfg.stateDir}/generated/dnsmasq.conf.fragment”
        仅当你明确希望直接采用生成片段时再开启，避免影响现有网络配置。
      '';
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
        ExecStart = lib.concatStringsSep " " (
          [ "${routerdPkg}/bin/routerd"
            "--state-dir ${cfg.stateDir}"
            "--addr ${cfg.address}"
          ]
          ++ lib.optional cfg.dev "--dev"
          ++ lib.optional cfg.applyReload "--apply-reload"
          ++ lib.optional cfg.applyTrafficControl "--apply-traffic-control"
        );
        DynamicUser = lib.mkDefault true;
        StateDirectory = "nixos-router";
        Restart = "on-failure";
        RestartSec = 2;
        AmbientCapabilities = [ ];
      } // lib.optionalAttrs cfg.privilegedApply {
        # 当需要执行 systemctl / tc 等运行态动作时，显式提权到 root。
        DynamicUser = lib.mkForce false;
        User = "root";
      };
      environment = cfg.environment // {
        NIXOS_ROUTER_STATE_DIR = cfg.stateDir;
        NIXOS_ROUTER_ADDR = cfg.address;
        NIXOS_ROUTER_DEV = lib.boolToString cfg.dev;
        NIXOS_ROUTER_ALLOW_REBOOT = lib.boolToString cfg.allowReboot;
        NIXOS_ROUTER_APPLY_TRAFFIC_CONTROL = lib.boolToString cfg.applyTrafficControl;
      };
    };

    # 可选：打开防火墙端口（简单实现：全局允许该 TCP 端口）
    # 若 address 未显式端口，默认回退到 8080。
    # 解析端口：匹配最后一个冒号后的数字；不支持 IPv6 字面量。
    networking.firewall.allowedTCPPorts = lib.mkIf cfg.openFirewall (
      let
        m = builtins.match ".*:(\\d+)$" cfg.address;
        port = if m == null then 8080 else builtins.fromJSON (builtins.head m);
      in [ port ]
    );

    # 可选：消费 dnsmasq 生成片段（不会自动启用/禁用 dnsmasq，仅追加配置）
    services.dnsmasq.extraConfig = lib.mkIf cfg.consumeGenerated
      ("conf-file=" + cfg.stateDir + "/generated/dnsmasq.conf.fragment");
  };
}
