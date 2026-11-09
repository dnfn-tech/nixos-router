{ config, lib, pkgs, ... }:
let
  cfg = config.services.nixos-router.backend;
  m = cfg.managed;
  socket = "/run/nixos-router-executor/control.sock";
  executorArgs = [ "${cfg.effectivePackage}/bin/router-executor"
    "--state-dir" cfg.stateDir "--socket" socket "--api-user" "nixos-router-api"
    "--bootstrap" "%d/bootstrap" "--interfaces" (lib.concatStringsSep "," m.interfaces)
    "--wifi-interface" m.wifiInterface "--country-code" m.countryCode
    "--management-port" (toString port)
    "--ssh-ports" (lib.concatMapStringsSep "," toString sshPorts) ];
  # ListenAddress may bind a port absent from the global Port directives.
  # Preserve every host SSH listener when optional WAN filtering is disabled.
  sshPorts = lib.unique (config.services.openssh.ports
    ++ map (address: address.port)
      (lib.filter (address: address.port != null) config.services.openssh.listenAddresses));
  current = "${cfg.stateDir}/managed/current";
  matchPort = builtins.match ".*:([0-9]+)$" cfg.address;
  port = if matchPort == null then 8080 else builtins.fromJSON (builtins.head matchPort);
  pppHook = action: pkgs.writeShellScript "routerd-ppp-${action}" ''
    set -eu
    test "''${1:-}" = nr-ppp0 || exit 1
    # net-init runs before authentication and passes ipparam as argument 4;
    # IPCP up/down pass it as argument 6.
    generation="${if action == "init" then "\${4:-}" else "\${6:-}"}"
    test -n "$generation" || exit 1
    test "$generation" = "$(${pkgs.coreutils}/bin/readlink ${lib.escapeShellArg current})" || exit 0
    test -s ${lib.escapeShellArg "${current}/pppoe.options"} || exit 0
    ${if action == "init" then ''
      ${pkgs.iproute2}/bin/ip link set dev "$1" alias nixos-router-managed-ppp
    '' else if action == "pre-up" then ''
      # LCP has set the negotiated transmit MTU. This synchronous hook runs
      # before IPCP enables traffic, so both SYN directions are clamped first.
      read -r mtu < /sys/class/net/nr-ppp0/mtu
      case "$mtu" in ""|*[!0-9]*) exit 1;; esac
      test "$mtu" -ge 128 && test "$mtu" -le 1492 || exit 1
      mss=$((mtu - 40))
      printf '%s\n' \
        'flush chain inet nixos_router ppp_mss' \
        "add rule inet nixos_router ppp_mss iifname \"nr-ppp0\" tcp flags & (syn | rst) == syn tcp option maxseg size > $mss tcp option maxseg size set $mss" \
        "add rule inet nixos_router ppp_mss oifname \"nr-ppp0\" tcp flags & (syn | rst) == syn tcp option maxseg size > $mss tcp option maxseg size set $mss" \
        | ${pkgs.nftables}/bin/nft -f -
    '' else if action == "up" then ''
      ${pkgs.iproute2}/bin/ip -4 route replace default dev "$1" proto static
      umask 077
      temp=${lib.escapeShellArg "${cfg.stateDir}/managed/wan-resolv.conf"}.tmp.$$
      trap '${pkgs.coreutils}/bin/rm -f "$temp"' EXIT
      : > "$temp"
      for server in "''${DNS1:-}" "''${DNS2:-}"; do
        case "$server" in *[!0-9.]*|"") continue;; esac
        printf 'nameserver %s\n' "$server" >> "$temp"
      done
      ${pkgs.coreutils}/bin/mv "$temp" ${lib.escapeShellArg "${cfg.stateDir}/managed/wan-resolv.conf"}
    '' else ''
      ${pkgs.iproute2}/bin/ip -4 route flush dev "$1" proto static || true
      umask 077
      temp=${lib.escapeShellArg "${cfg.stateDir}/managed/wan-resolv.conf"}.tmp.$$
      : > "$temp"
      ${pkgs.coreutils}/bin/mv "$temp" ${lib.escapeShellArg "${cfg.stateDir}/managed/wan-resolv.conf"}
    ''}
  '';
  dhcpHook = pkgs.writeShellScript "routerd-dhcp-hook" ''
    set -eu
    set -f
    # Old lease callbacks must not overwrite a new static configuration.
    test -n "''${WAN_GENERATION:-}" || exit 1
    test "''${interface:-}" = "''${WAN_INTERFACE:-}" || exit 1
    test -f ${lib.escapeShellArg "${current}/wan.env"} || exit 0
    ${pkgs.gnugrep}/bin/grep -Fx "WAN_GENERATION='$WAN_GENERATION'" ${lib.escapeShellArg "${current}/wan.env"} >/dev/null || exit 0
    case "$1" in
      deconfig)
        ${pkgs.iproute2}/bin/ip -4 address flush dev "$interface" scope global
        ${pkgs.iproute2}/bin/ip -4 route flush dev "$interface" proto dhcp
        ;;
      bound|renew)
        prefix=$(${pkgs.busybox}/bin/ipcalc -p "$ip" "$subnet")
        prefix=''${prefix#PREFIX=}
        ${pkgs.iproute2}/bin/ip -4 address flush dev "$interface" scope global
        ${pkgs.iproute2}/bin/ip -4 address add "$ip/$prefix" dev "$interface"
        ${pkgs.iproute2}/bin/ip -4 route flush dev "$interface" proto dhcp
        if test -n "''${router:-}"; then
          set -- $router
          ${pkgs.iproute2}/bin/ip -4 route replace default via "$1" dev "$interface" proto dhcp
        fi
        umask 077
        temp=${lib.escapeShellArg "${cfg.stateDir}/managed/wan-resolv.conf"}.tmp.$$
        : > "$temp"
        for server in ''${dns:-}; do
          case "$server" in *[!0-9.]*|"") continue;; esac
          printf 'nameserver %s\n' "$server" >> "$temp"
        done
        mv "$temp" ${lib.escapeShellArg "${cfg.stateDir}/managed/wan-resolv.conf"}
        ;;
    esac
  '';
in {
  options.services.nixos-router.backend.managed = {
    apiStateDir = lib.mkOption { type = lib.types.path; default = "${cfg.stateDir}-api"; description = "Separate low-privilege API state. Migrated once from stateDir; must not overlap the private executor directory."; };
    enable = lib.mkEnableOption "explicit ownership of router interfaces (hardware-test candidate)";
    interfaces = lib.mkOption { type = lib.types.listOf lib.types.str; default = []; description = "Explicit allowlist including physical wired WAN and LAN members. Never include a management/control interface owned elsewhere."; };
    wifiInterface = lib.mkOption { type = lib.types.str; default = ""; description = "Explicit physical AP interface, separate from the wired interfaces allowlist."; };
    countryCode = lib.mkOption { type = lib.types.str; default = "CN"; description = "Regulatory country for the dedicated AP."; };
    bootstrapConfigFile = lib.mkOption { type = lib.types.str; default = ""; description = "Runtime path to initial JSON, copied only when config.json does not exist. Keep real secrets outside the Nix store."; };
    adminPasswordFile = lib.mkOption { type = lib.types.str; default = ""; description = "Runtime secret file for initial administrator creation; ignored after an account exists."; };
  };
  config = lib.mkIf (cfg.enable && m.enable) {
    assertions = [
      { assertion = m.apiStateDir != cfg.stateDir && !(lib.hasPrefix "${cfg.stateDir}/" m.apiStateDir) && !(lib.hasPrefix "${m.apiStateDir}/" cfg.stateDir); message = "nixos-router API and executor state directories must not overlap."; }
      { assertion = !cfg.allowReboot; message = "nixos-router managed API does not yet support a privileged reboot RPC; leave allowReboot disabled."; }
      { assertion = m.interfaces != []; message = "nixos-router managed mode requires an explicit interface allowlist."; }
      { assertion = m.bootstrapConfigFile != ""; message = "nixos-router managed mode requires an explicit bootstrapConfigFile."; }
      { assertion = !cfg.dev; message = "nixos-router managed mode forbids unauthenticated dev mode."; }
      { assertion = !cfg.openFirewall; message = "nixos-router managed mode restricts management via its owned inet firewall; do not globally open the port."; }
      { assertion = !config.services.kea.dhcp4.enable && !config.services.dnsmasq.enable; message = "nixos-router managed mode owns DHCP/DNS; disable other Kea/dnsmasq services explicitly."; }
      { assertion = !config.services.hostapd.enable; message = "nixos-router managed mode owns hostapd; disable the previous AP service explicitly."; }
      { assertion = !config.networking.firewall.enable && !config.networking.nat.enable && !config.networking.nftables.enable; message = "nixos-router managed mode owns firewall/NAT; disable NixOS firewall, NAT and nftables service explicitly."; }
      { assertion = lib.all (name: let i = config.networking.interfaces.${name} or {}; in (i.ipv4.addresses or []) == [] && (i.useDHCP or false) != true) (m.interfaces ++ lib.optional (m.wifiInterface != "") m.wifiInterface); message = "nixos-router managed interfaces must not have NixOS-managed addresses or DHCP clients."; }
    ];
    services.nixos-router.backend = {
      applyReload = lib.mkForce false;
      privilegedApply = lib.mkForce false;
      applyTrafficControl = lib.mkForce false;
      consumeGenerated = lib.mkForce false;
    };
    boot.kernel.sysctl."net.ipv4.ip_forward" = 1;
    boot.kernelModules = [ "ppp_generic" "pppoe" ];
    users.groups.nixos-router-api = {};
    users.users.nixos-router-api = { isSystemUser = true; group = "nixos-router-api"; };
    systemd.services.nixos-router-state-init = {
      description = "Prepare private router API state without moving runtime configuration";
      before = [ "nixos-router-executor.service" "nixos-router-backend.service" ];
      after = [ "local-fs.target" ];
      serviceConfig = {
        Type = "oneshot";
        RemainAfterExit = true;
        User = "root";
        UMask = "0077";
        LoadCredential = [ "bootstrap:${m.bootstrapConfigFile}" ];
        ExecStart = lib.escapeShellArgs (executorArgs ++ [ "--prepare-api-state" m.apiStateDir ]);
      };
    };
    systemd.services.nixos-router-executor = {
      description = "Privileged router network executor";
      wantedBy = [ "multi-user.target" ];
      requires = [ "nixos-router-state-init.service" ];
      after = [ "local-fs.target" "nixos-router-state-init.service" ];
      before = [ "nixos-router-backend.service" ];
      path = [ pkgs.systemd pkgs.nftables pkgs.iproute2 pkgs.busybox pkgs.dnsmasq pkgs.hostapd pkgs.iw ];
      environment.NIXOS_ROUTER_MANAGED_SSH_PORTS = lib.concatMapStringsSep "," toString sshPorts;
      serviceConfig = {
        Type = "notify";
        NotifyAccess = "main";
        User = "root";
        UMask = "0077";
        RuntimeDirectory = "nixos-router-executor";
        RuntimeDirectoryMode = "0700";
        LoadCredential = [ "bootstrap:${m.bootstrapConfigFile}" ];
        ExecStart = lib.escapeShellArgs executorArgs;
        Restart = "on-failure";
        RestartSec = 2;
        TimeoutStartSec = 360;
        TimeoutStopSec = 360;
        PrivateTmp = true;
        NoNewPrivileges = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        ReadWritePaths = [ cfg.stateDir "/run/nixos-router-executor" ];
      };
    };
    systemd.services.nixos-router-backend = {
      after = lib.mkForce [ "nixos-router-executor.service" "nixos-router-state-init.service" ];
      requires = [ "nixos-router-state-init.service" ];
      wants = [ "nixos-router-executor.service" ];
      path = [ pkgs.iproute2 pkgs.iw ];
      environment = {
        NIXOS_ROUTER_MANAGED = "1";
        NIXOS_ROUTER_EXECUTOR_SOCKET = socket;
        NIXOS_ROUTER_WIFI_INTERFACE = m.wifiInterface;
        NIXOS_ROUTER_ADMIN_PASSWORD_FILE = if m.adminPasswordFile == "" then "" else "%d/admin-password";
      };
      serviceConfig = {
        User = "nixos-router-api";
        Group = "nixos-router-api";
        DynamicUser = lib.mkForce false;
        StateDirectory = lib.mkForce [];
        UMask = "0077";
        NoNewPrivileges = true;
        CapabilityBoundingSet = [];
        AmbientCapabilities = lib.mkForce [];
        ProtectSystem = "strict";
        ProtectHome = true;
        PrivateTmp = true;
        PrivateDevices = true;
        ProtectKernelTunables = true;
        ProtectKernelModules = true;
        ProtectControlGroups = true;
        RestrictSUIDSGID = true;
        RestrictAddressFamilies = [ "AF_UNIX" "AF_INET" "AF_INET6" "AF_NETLINK" ];
        ReadWritePaths = [ m.apiStateDir ];
        LoadCredential = lib.optional (m.adminPasswordFile != "") "admin-password:${m.adminPasswordFile}";
      };
    };
    systemd.services.nixos-router-wan = {
      description = "routerd dedicated WAN DHCP protocol client";
      serviceConfig = {
        Type = "simple";
        EnvironmentFile = "${current}/wan.env";
        ExecStart = "${pkgs.busybox}/bin/udhcpc -f -i $WAN_INTERFACE -s ${dhcpHook} -R";
        Restart = "on-failure";
        RestartSec = 2;
        KillMode = "control-group";
        TimeoutStopSec = 10;
      };
    };
    systemd.services.nixos-router-pppoe = {
      description = "routerd dedicated WAN PPPoE protocol client";
      serviceConfig = {
        Type = "simple";
        User = "root";
        UMask = "0077";
        ExecStart = "${pkgs.ppp}/sbin/pppd file ${current}/pppoe.options usepeerdns net-init-script ${pppHook "init"} ip-pre-up-script ${pppHook "pre-up"} ip-up-script ${pppHook "up"} ip-down-script ${pppHook "down"}";
        Restart = "always";
        RestartSec = 5;
        KillMode = "control-group";
        TimeoutStopSec = 15;
        PrivateTmp = true;
        ProtectHome = true;
        ProtectSystem = "strict";
        ReadWritePaths = [ cfg.stateDir "/run" ];
        # Isolate global options, default hooks and generated resolv.conf.
        TemporaryFileSystem = [ "/etc/ppp:rw,mode=0700" ];
      };
    };
    systemd.services.nixos-router-dnsmasq = {
      description = "routerd dedicated LAN DHCP/DNS";
      serviceConfig = {
        Type = "forking";
        PIDFile = "${cfg.stateDir}/managed/dnsmasq.pid";
        # dnsmasq daemonizes only after initial binding succeeds. Keep root so
        # atomic WAN resolver updates remain readable inside private state.
        ExecStart = "${pkgs.dnsmasq}/bin/dnsmasq --user=root --pid-file=${cfg.stateDir}/managed/dnsmasq.pid --conf-file=${current}/dnsmasq.conf";
        Restart = "on-failure";
        RestartSec = 2;
        UMask = "0077";
      };
    };
    systemd.services.nixos-router-hostapd = {
      description = "routerd dedicated wireless AP";
      serviceConfig = {
        Type = "simple";
        ExecStart = "${pkgs.hostapd}/bin/hostapd ${current}/hostapd.conf";
        Restart = "on-failure";
        RestartSec = 2;
        UMask = "0077";
      };
    };
  };
}
