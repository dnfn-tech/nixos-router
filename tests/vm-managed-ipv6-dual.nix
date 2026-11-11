# A real DHCPv6/PD ISP and wired client plus a hwsim WPA3 station inside
# the CPE VM. All fixture secrets are deliberately public synthetic values.
{ pkgs, self, advancedRadio ? false }:
let
  base = import ./vm-managed-ipv6.nix { inherit pkgs self; };
  station = pkgs.writeText "dual-ipv6-station.conf" ''
    ctrl_interface=/run/dual-station-control
    network={
      ssid="dual-ipv6-test"
      key_mgmt=SAE
      ieee80211w=2
      sae_password="synthetic-v6-wifi-password"
    }
  '';
  dhcpHook = pkgs.writeShellScript "dual-station-dhcp" ''
    set -eu
    case "$1" in
      deconfig) ${pkgs.iproute2}/bin/ip -4 address flush dev "$interface" scope global ;;
      bound|renew)
        prefix=$(${pkgs.busybox}/bin/ipcalc -p "$ip" "$subnet")
        prefix=''${prefix#PREFIX=}
        ${pkgs.iproute2}/bin/ip -4 address flush dev "$interface" scope global
        ${pkgs.iproute2}/bin/ip address add "$ip/$prefix" dev "$interface"
        ${pkgs.iproute2}/bin/ip route replace default via "''${router%% *}" dev "$interface"
        printf '%s\n' "$ip" > /run/dual-station-ipv4
        ;;
    esac
  '';
in base // {
  name = "vm-managed-ipv6-dual" + pkgs.lib.optionalString advancedRadio "-radio";
  nodes = {
    inherit (base.nodes) client;
    upstream = { lib, config, ... }: {
      imports = [ base.nodes.upstream ];
      # Mutate only this disposable ISP's runtime config to exercise real
      # renumbering and insufficient PD; never fabricate CPE lease callbacks.
      systemd.services.kea-dhcp6-server = {
        preStart = ''
          if ! test -f /var/lib/kea/test-config.json; then
            ${pkgs.coreutils}/bin/cp /etc/kea/dhcp6-server.conf /var/lib/kea/test-config.json
          fi
        '';
        serviceConfig.ExecStart = lib.mkForce "${lib.getExe' config.services.kea.package "kea-dhcp6"} -c /var/lib/kea/test-config.json";
      };
    };
    router = { lib, ... }: {
      imports = [ base.nodes.router ];
      virtualisation.memorySize = lib.mkForce 1536;
      virtualisation.cores = lib.mkForce 2;
      networking.usePredictableInterfaceNames = false;
      networking.wireless.enable = lib.mkForce false;
      boot.kernelModules = [ "mac80211_hwsim" ];
      boot.extraModprobeConfig = "options mac80211_hwsim radios=2";
      hardware.wirelessRegulatoryDatabase = true;
      environment.systemPackages = with pkgs; [ iw wpa_supplicant busybox ];
      environment.etc."dual-station-dhcp".source = dhcpHook;
      services.nixos-router.backend.managed = {
        wifiInterface = "wlan0";
        countryCode = "US";
      };
      systemd.tmpfiles.rules = [
        "d /run/wpa_supplicant 0755 root root -"
        "d /run/wpa_supplicant/client 0700 root root -"
      ];
      systemd.services.dual-radio-links = {
        before = [ "nixos-router-executor.service" ];
        requiredBy = [ "nixos-router-executor.service" ];
        after = [ "systemd-modules-load.service" ];
        path = [ pkgs.iproute2 pkgs.iw pkgs.systemd pkgs.kmod pkgs.procps ];
        serviceConfig = { Type = "oneshot"; RemainAfterExit = true; };
        script = ''
          modprobe mac80211_hwsim radios=2
          udevadm settle
          test -d /sys/class/net/wlan0
          test -d /sys/class/net/wlan1
          iw reg set US
          ip netns add radio-station
          iw phy phy1 set netns name radio-station
          ip -n radio-station link set lo up
          ip netns exec radio-station sysctl -qw net/ipv6/conf/wlan1/accept_ra=2 net/ipv6/conf/wlan1/autoconf=1
          ip -n radio-station link set wlan1 up
        '';
      };
      systemd.services.dual-radio-station = {
        wantedBy = [ "multi-user.target" ];
        requires = [ "dual-radio-links.service" ];
        after = [ "dual-radio-links.service" ];
        serviceConfig = {
          ExecStart = "${pkgs.iproute2}/bin/ip netns exec radio-station ${pkgs.wpa_supplicant}/bin/wpa_supplicant -D nl80211 -i wlan1 -c ${station}";
          Restart = "on-failure";
        };
      };
    };
  };
  testScript = "RADIO_ADVANCED = ${if advancedRadio then "True" else "False"}\n" + builtins.readFile ./managed-ipv6-dual.py;
}
