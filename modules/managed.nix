{ config, lib, pkgs, ... }:
let
  cfg = config.services.nixos-router.backend;
  m = cfg.managed;
  # ListenAddress may bind a port absent from the global Port directives.
  # Preserve every host SSH listener when optional WAN filtering is disabled.
  sshPorts = lib.unique (config.services.openssh.ports
    ++ map (address: address.port)
      (lib.filter (address: address.port != null) config.services.openssh.listenAddresses));
  current = "${cfg.stateDir}/managed/current";
  matchPort = builtins.match ".*:([0-9]+)$" cfg.address;
  port = if matchPort == null then 8080 else builtins.fromJSON (builtins.head matchPort);
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
    enable = lib.mkEnableOption "explicit ownership of router interfaces (hardware-test candidate)";
    interfaces = lib.mkOption { type = lib.types.listOf lib.types.str; default = []; description = "Explicit allowlist including physical wired WAN and LAN members. Never include a management/control interface owned elsewhere."; };
    wifiInterface = lib.mkOption { type = lib.types.str; default = ""; description = "Explicit physical AP interface, separate from the wired interfaces allowlist."; };
    countryCode = lib.mkOption { type = lib.types.str; default = "CN"; description = "Regulatory country for the dedicated AP."; };
    bootstrapConfigFile = lib.mkOption { type = lib.types.str; default = ""; description = "Runtime path to initial JSON, copied only when config.json does not exist. Keep real secrets outside the Nix store."; };
    adminPasswordFile = lib.mkOption { type = lib.types.str; default = ""; description = "Runtime secret file for initial administrator creation; ignored after an account exists."; };
  };
  config = lib.mkIf (cfg.enable && m.enable) {
    assertions = [
      { assertion = m.interfaces != []; message = "nixos-router managed mode requires an explicit interface allowlist."; }
      { assertion = m.bootstrapConfigFile != ""; message = "nixos-router managed mode requires an explicit bootstrapConfigFile."; }
      { assertion = !cfg.dev; message = "nixos-router managed mode forbids unauthenticated dev mode."; }
      { assertion = !cfg.openFirewall; message = "nixos-router managed mode restricts management via its owned inet firewall; do not globally open the port."; }
      { assertion = !config.services.kea.dhcp4.enable && !config.services.dnsmasq.enable; message = "nixos-router managed mode owns DHCP/DNS; disable other Kea/dnsmasq services explicitly."; }
      { assertion = !config.services.hostapd.enable; message = "nixos-router managed mode owns hostapd; disable the previous AP service explicitly."; }
      { assertion = !config.networking.firewall.enable && !config.networking.nat.enable && !config.networking.nftables.enable; message = "nixos-router managed mode owns firewall/NAT; disable NixOS firewall, NAT and nftables service explicitly."; }
      { assertion = lib.all (name: let i = config.networking.interfaces.${name} or {}; in (i.ipv4.addresses or []) == [] && (i.useDHCP or false) != true) m.interfaces; message = "nixos-router managed interfaces must not have NixOS-managed addresses or DHCP clients."; }
    ];
    services.nixos-router.backend = {
      applyReload = true;
      privilegedApply = true;
      consumeGenerated = lib.mkForce false;
    };
    boot.kernel.sysctl."net.ipv4.ip_forward" = 1;
    systemd.services.nixos-router-backend = {
      after = lib.mkForce [ "local-fs.target" ];
      path = [ pkgs.busybox pkgs.dnsmasq pkgs.hostapd pkgs.iw ];
      preStart = ''
        install -d -m 0700 ${lib.escapeShellArg cfg.stateDir}
        if ! test -e ${lib.escapeShellArg "${cfg.stateDir}/config.json"}; then
          install -m 0600 ${lib.escapeShellArg m.bootstrapConfigFile} ${lib.escapeShellArg "${cfg.stateDir}/config.json"}
        fi
      '';
      environment = {
        NIXOS_ROUTER_MANAGED = "1";
        NIXOS_ROUTER_MANAGED_INTERFACES = lib.concatStringsSep "," m.interfaces;
        NIXOS_ROUTER_MANAGED_SSH_PORTS = lib.concatMapStringsSep "," toString sshPorts;
        NIXOS_ROUTER_WIFI_INTERFACE = m.wifiInterface;
        NIXOS_ROUTER_COUNTRY_CODE = m.countryCode;
        NIXOS_ROUTER_MANAGEMENT_PORT = toString port;
        NIXOS_ROUTER_ADMIN_PASSWORD_FILE = m.adminPasswordFile;
        NIXOS_ROUTER_DNSMASQ_LEASES = "${cfg.stateDir}/dnsmasq.leases";
      };
      serviceConfig.UMask = "0077";
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
