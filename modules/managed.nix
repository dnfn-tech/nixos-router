{ config, lib, pkgs, ... }:
let
  cfg = config.services.nixos-router.backend;
  m = cfg.managed;
  managedDnsmasq = import ./managed-dnsmasq.nix { inherit pkgs; };
  socket = "/run/nixos-router-executor/control.sock";
  executorArgs = [ "${cfg.effectivePackage}/bin/router-executor"
    "--state-dir" cfg.stateDir "--socket" socket "--api-user" "nixos-router-api"
    "--bootstrap" "%d/bootstrap" "--interfaces" (lib.concatStringsSep "," m.interfaces)
    "--wifi-interface" m.wifiInterface "--country-code" m.countryCode
    "--management-port" (toString port)
    "--ssh-ports" (lib.concatMapStringsSep "," toString sshPorts)
    "--wan-udp-ports" (lib.concatMapStringsSep "," toString m.wanUDPPorts)
    "--modules" (lib.concatStringsSep "," m.modules) ];
  # ListenAddress may bind a port absent from the global Port directives.
  # Preserve every host SSH listener when optional WAN filtering is disabled.
  sshPorts = lib.optionals config.services.openssh.enable (lib.unique (config.services.openssh.ports
    ++ map (address: address.port)
      (lib.filter (address: address.port != null) config.services.openssh.listenAddresses)));
  current = "${cfg.stateDir}/managed/current";
  matchPort = builtins.match ".*:([0-9]+)$" cfg.address;
  port = if matchPort == null then 8080 else builtins.fromJSON (builtins.head matchPort);
  wan6Hook = pkgs.writeShellScript "routerd-wan6-hook" ''
    set -eu
    test "''${1:-}" = "''${WAN6_INTERFACE:-}" || exit 1
    exec ${lib.escapeShellArgs [ "${cfg.effectivePackage}/bin/router-executor" "ipv6-event"
      "--state-dir" cfg.stateDir "--interfaces" (lib.concatStringsSep "," m.interfaces)
      "--wifi-interface" m.wifiInterface "--management-port" (toString port) ]} \
      --generation "$WAN6_GENERATION" --interface "$1" --event "$2"
  '';
  wan6Start = pkgs.writeShellScript "routerd-wan6-start" ''
    set -eu
    case "$WAN6_MODE" in
      slaac) set -- -N none ;;
      dhcpv6|pppoe6) set -- -S -N try ;;
      *) exit 1 ;;
    esac
    case "$WAN6_PD" in
      0) ;;
      1) set -- "$@" -P 0 ;;
      *) exit 1 ;;
    esac
    case "$WAN6_INTERFACE" in
      ${lib.concatMapStringsSep "|" lib.escapeShellArg (m.interfaces ++ [ "nr-ppp0" ])}) ;;
      *) exit 1 ;;
    esac
    # PPP recreates its link on every reconnect. Set kernel ownership before
    # each client start, including those triggered by IPv6CP rather than Apply.
    for setting in accept_ra=0 autoconf=0 use_tempaddr=0 accept_redirects=0 disable_ipv6=0; do
      ${pkgs.procps}/bin/sysctl -qw "net/ipv6/conf/$WAN6_INTERFACE/$setting"
    done
    if test "$WAN6_PD" = 1; then
      ${pkgs.procps}/bin/sysctl -qw "net/ipv6/conf/$WAN6_INTERFACE/forwarding=1"
      ${pkgs.procps}/bin/sysctl -qw "net/ipv6/conf/$WAN6_INTERFACE/force_forwarding=1"
    else
      ${pkgs.procps}/bin/sysctl -eqw "net/ipv6/conf/$WAN6_INTERFACE/force_forwarding=0"
      ${pkgs.procps}/bin/sysctl -eqw "net/ipv6/conf/$WAN6_INTERFACE/forwarding=0"
    fi
    exec ${pkgs.odhcp6c}/bin/odhcp6c -e -s ${wan6Hook} \
      -p /run/nixos-router-wan6/client.pid "$@" "$WAN6_INTERFACE"
  '';
  dnsCallback = pkgs.writeShellScript "routerd-dns-callback" ''
    set -eu
    exec ${lib.escapeShellArgs [ "${cfg.effectivePackage}/bin/router-executor" "dns-event"
      "--state-dir" cfg.stateDir "--interfaces" (lib.concatStringsSep "," m.interfaces)
      "--wifi-interface" m.wifiInterface "--management-port" (toString port) ]} \
      --generation "$1" --interface "$2" --event "$3"
  '';
  wanStop = pkgs.writeShellScript "routerd-dhcp-stop" ''
    set -eu
    test -n "''${WAN_GENERATION:-}" || exit 0
    NIXOS_ROUTER_IPV4_DNS= exec ${dnsCallback} "$WAN_GENERATION" "$WAN_INTERFACE" unbound
  '';
  pppHook = action: pkgs.writeShellScript "routerd-ppp-${action}" ''
    set -eu
    test "''${1:-}" = nr-ppp0 || exit 1
    # net-init and net-pre-up pass ipparam as argument 4;
    # IPCP and IPv6CP up/down pass it as argument 6.
    generation="${if action == "init" || action == "pre-up" then "\${4:-}" else "\${6:-}"}"
    test -n "$generation" || exit 1
    test "$generation" = "$(${pkgs.coreutils}/bin/readlink ${lib.escapeShellArg current})" || exit 0
    test -s ${lib.escapeShellArg "${current}/pppoe.options"} || exit 0
    ${if action == "init" then ''
      ${pkgs.iproute2}/bin/ip link set dev "$1" alias nixos-router-managed-ppp
      # net-init is synchronous and runs while the new PPP link is still
      # down. Waiting for IPv6-up/wan6 permits the peer's first RA to create
      # kernel-owned SLAAC addresses/routes before our ownership is set.
      for setting in accept_ra=0 autoconf=0 use_tempaddr=0 accept_redirects=0; do
        ${pkgs.procps}/bin/sysctl -qw "net/ipv6/conf/$1/$setting"
      done
    '' else if action == "pre-up" then ''
      # LCP has set the negotiated transmit MTU. net-pre-up runs before
      # either IPCP or IPv6CP, unlike ip-pre-up which only gates IPv4.
      read -r mtu < /sys/class/net/nr-ppp0/mtu
      case "$mtu" in ""|*[!0-9]*) exit 1;; esac
      test "$mtu" -ge 128 && test "$mtu" -le 1492 || exit 1
      mss=$((mtu - 40))
      mss6=$((mtu - 60))
      printf '%s\n' \
        'flush chain inet nixos_router ppp_mss' \
        "add rule inet nixos_router ppp_mss meta nfproto ipv4 iifname \"nr-ppp0\" tcp flags & (syn | rst) == syn tcp option maxseg size > $mss tcp option maxseg size set $mss" \
        "add rule inet nixos_router ppp_mss meta nfproto ipv4 oifname \"nr-ppp0\" tcp flags & (syn | rst) == syn tcp option maxseg size > $mss tcp option maxseg size set $mss" \
        "add rule inet nixos_router ppp_mss meta nfproto ipv6 iifname \"nr-ppp0\" tcp flags & (syn | rst) == syn tcp option maxseg size > $mss6 tcp option maxseg size set $mss6" \
        "add rule inet nixos_router ppp_mss meta nfproto ipv6 oifname \"nr-ppp0\" tcp flags & (syn | rst) == syn tcp option maxseg size > $mss6 tcp option maxseg size set $mss6" \
        | ${pkgs.nftables}/bin/nft -f -
    '' else if action == "ipv6-up" || action == "ipv6-down" then ''
      # pppd does not inherit the service environment into its scripts.
      # generation was matched to the private current pointer above.
      . ${lib.escapeShellArg "${cfg.stateDir}/managed"}/"$generation"/wan6.env
      test "''${WAN6_ENABLED:-0}" = 1 || exit 0
      test "''${WAN6_MODE:-}" = pppoe6 || exit 0
      test "$generation" = "''${WAN6_GENERATION:-}" || exit 0
      exec ${pkgs.systemd}/bin/systemctl ${if action == "ipv6-up" then "restart" else "--no-block stop"} nixos-router-wan6.service
    '' else if action == "up" then ''
      ${pkgs.iproute2}/bin/ip -4 route replace default dev "$1" proto static
      NIXOS_ROUTER_IPV4_DNS="''${DNS1:-} ''${DNS2:-}" ${dnsCallback} "$generation" "$1" bound
    '' else ''
      ${pkgs.iproute2}/bin/ip -4 route flush dev "$1" proto static || true
      NIXOS_ROUTER_IPV4_DNS= ${dnsCallback} "$generation" "$1" unbound
    ''}
  '';
  pppStop = pkgs.writeShellScript "routerd-ppp-stop" ''
    set -eu
    test "''${WAN6_GENERATION:-}" = "$(${pkgs.coreutils}/bin/readlink ${lib.escapeShellArg current})" || exit 0
    NIXOS_ROUTER_IPV4_DNS= ${dnsCallback} "$WAN6_GENERATION" nr-ppp0 unbound
    test "''${WAN6_ENABLED:-0}" = 1 || exit 0
    test "''${WAN6_MODE:-}" = pppoe6 || exit 0
    # Queue the child stop without waiting inside another service stop job.
    # Apply independently waits for WAN6 before removing links or switching files.
    exec ${pkgs.systemd}/bin/systemctl --no-block stop nixos-router-wan6.service
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
        NIXOS_ROUTER_IPV4_DNS= ${dnsCallback} "$WAN_GENERATION" "$interface" unbound
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
        NIXOS_ROUTER_IPV4_DNS="''${dns:-}" ${dnsCallback} "$WAN_GENERATION" "$interface" bound
        ;;
    esac
  '';
in {
  options.services.nixos-router.backend.managed = {
    modules = lib.mkOption { type = lib.types.listOf (lib.types.enum [ "adblock" ]); default = [ "adblock" ]; description = "Allowed compiled-in modules, shared by API and executor. Empty disables optional runtime modules."; };
    apiStateDir = lib.mkOption { type = lib.types.path; default = "${cfg.stateDir}-api"; description = "Separate low-privilege API state. Migrated once from stateDir; must not overlap the private executor directory."; };
    enable = lib.mkEnableOption "explicit ownership of router interfaces (hardware-test candidate)";
    interfaces = lib.mkOption { type = lib.types.listOf lib.types.str; default = []; description = "Explicit allowlist including physical wired WAN and LAN members. Never include a management/control interface owned elsewhere."; };
    wifiInterface = lib.mkOption { type = lib.types.str; default = ""; description = "Explicit physical AP interface, separate from the wired interfaces allowlist."; };
    wanUDPPorts = lib.mkOption {
      type = lib.types.listOf lib.types.port;
      default = [];
      description = "Trusted host UDP listeners allowed on the actual WAN exit (both address families), at most 64 entries. Does not trust overlay interfaces or permit forwarded traffic. Existing established connections follow connection tracking when an entry is removed.";
    };
    countryCode = lib.mkOption { type = lib.types.str; default = "CN"; description = "Regulatory country for the dedicated AP."; };
    bootstrapConfigFile = lib.mkOption { type = lib.types.str; default = ""; description = "Runtime path to initial JSON, copied only when config.json does not exist. Keep real secrets outside the Nix store."; };
    adminPasswordFile = lib.mkOption { type = lib.types.str; default = ""; description = "Runtime secret file for initial administrator creation; ignored after an account exists."; };
  };
  config = lib.mkIf (cfg.enable && m.enable) {
    assertions = [
      { assertion = builtins.length m.wanUDPPorts <= 64 && lib.all (port: port >= 1) m.wanUDPPorts; message = "nixos-router managed WAN UDP allowlist permits at most 64 ports in 1..65535."; }
      { assertion = m.apiStateDir != cfg.stateDir && !(lib.hasPrefix "${cfg.stateDir}/" m.apiStateDir) && !(lib.hasPrefix "${m.apiStateDir}/" cfg.stateDir); message = "nixos-router API and executor state directories must not overlap."; }
      { assertion = !cfg.allowReboot; message = "nixos-router managed API does not yet support a privileged reboot RPC; leave allowReboot disabled."; }
      { assertion = m.interfaces != []; message = "nixos-router managed mode requires an explicit interface allowlist."; }
      { assertion = m.bootstrapConfigFile != ""; message = "nixos-router managed mode requires an explicit bootstrapConfigFile."; }
      { assertion = !cfg.dev; message = "nixos-router managed mode forbids unauthenticated dev mode."; }
      { assertion = !cfg.openFirewall; message = "nixos-router managed mode restricts management via its owned inet firewall; do not globally open the port."; }
      { assertion = !config.services.kea.dhcp4.enable && !config.services.kea.dhcp6.enable && !config.services.dnsmasq.enable; message = "nixos-router managed mode owns DHCP/DNS; disable other Kea/dnsmasq services explicitly."; }
      { assertion = !config.services.radvd.enable; message = "nixos-router managed mode owns IPv6 router advertisements; disable the previous radvd service explicitly."; }
      { assertion = !config.services.hostapd.enable; message = "nixos-router managed mode owns hostapd; disable the previous AP service explicitly."; }
      { assertion = !config.networking.firewall.enable && !config.networking.nat.enable && !config.networking.nftables.enable; message = "nixos-router managed mode owns firewall/NAT; disable NixOS firewall, NAT and nftables service explicitly."; }
      { assertion = lib.all (name: let i = config.networking.interfaces.${name} or {}; in (i.ipv4.addresses or []) == [] && (i.ipv6.addresses or []) == [] && (i.useDHCP or false) != true) (m.interfaces ++ lib.optional (m.wifiInterface != "") m.wifiInterface); message = "nixos-router managed interfaces must not have NixOS-managed addresses or DHCP clients."; }
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
      path = [ pkgs.systemd pkgs.nftables pkgs.iproute2 pkgs.procps pkgs.busybox managedDnsmasq pkgs.hostapd pkgs.iw ];
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
        ExecStopPost = wanStop;
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
        NIXOS_ROUTER_MODULES = lib.concatStringsSep "," m.modules;
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
        ExecStopPost = wanStop;
        Restart = "on-failure";
        RestartSec = 2;
        KillMode = "control-group";
        TimeoutStopSec = 60;
      };
    };
    systemd.services.nixos-router-wan6 = {
      description = "routerd dedicated WAN RA and DHCPv6 client";
      path = [ pkgs.iproute2 pkgs.procps ];
      serviceConfig = {
        Type = "simple";
        User = "root";
        UMask = "0077";
        EnvironmentFile = "${current}/wan6.env";
        ExecCondition = "${pkgs.coreutils}/bin/test $WAN6_ENABLED = 1";
        ExecStart = wan6Start;
        # A crash or forced termination may not deliver odhcp6c's stopped
        # notification. Run cleanup only after every old callback has exited.
        ExecStopPost = "${wan6Hook} $WAN6_INTERFACE stopped";
        Restart = "always";
        RestartSec = 2;
        KillMode = "control-group";
        TimeoutStopSec = 60;
        RuntimeDirectory = "nixos-router-wan6";
        RuntimeDirectoryMode = "0700";
        PrivateTmp = true;
        ProtectHome = true;
        ProtectSystem = "strict";
        NoNewPrivileges = true;
        ReadWritePaths = [ cfg.stateDir "/run/nixos-router-wan6" ];
      };
    };
    systemd.services.nixos-router-pppoe = {
      description = "routerd dedicated WAN PPPoE protocol client";
      serviceConfig = {
        Type = "simple";
        User = "root";
        UMask = "0077";
        EnvironmentFile = "${current}/wan6.env";
        ExecStart = "${pkgs.ppp}/sbin/pppd file ${current}/pppoe.options usepeerdns net-init-script ${pppHook "init"} net-pre-up-script ${pppHook "pre-up"} ip-up-script ${pppHook "up"} ip-down-script ${pppHook "down"} ipv6-up-script ${pppHook "ipv6-up"} ipv6-down-script ${pppHook "ipv6-down"}";
        ExecStopPost = pppStop;
        Restart = "always";
        RestartSec = 5;
        KillMode = "control-group";
        TimeoutStopSec = 60;
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
        ExecStart = "${managedDnsmasq}/bin/dnsmasq --user=root --pid-file=${cfg.stateDir}/managed/dnsmasq.pid --conf-file=${current}/dnsmasq.conf";
        ExecStopPost = wanStop;
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
        ExecStopPost = wanStop;
        Restart = "on-failure";
        RestartSec = 2;
        UMask = "0077";
      };
    };
  };
}
