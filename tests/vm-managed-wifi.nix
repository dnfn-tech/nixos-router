# Software-only WPA2/DHCP acceptance. Real RF, MT7927 firmware, throughput and
# regulatory compliance remain separate hardware acceptance requirements.
{ pkgs, self }:
let
  bootstrap = pkgs.writeText "managed-wifi-bootstrap.json" (builtins.toJSON {
    system = { hostname = "wifi-router"; timezone = "UTC"; };
    wan = { interface = "wan0"; mode = "static"; static.addressCidr = "10.99.0.2/24"; };
    lan = {
      bridgeName = "br-lan"; ipv4Cidr = "192.168.77.1/24"; ports = [ "lan0" ];
      dhcp = { enable = true; rangeStart = "192.168.77.100"; rangeEnd = "192.168.77.110"; leaseMins = 30; };
    };
    dns = { enableDnsmasq = true; domain = "lan"; upstreams = [ "10.99.0.1" ]; };
    wifi = {
      enable = false; bridgeToLan = true;
      aps = [{ enable = true; ssid = "managed-wifi-test"; band = "2g"; channel = 6; psk = "synthetic-wifi-password"; }];
    };
    firewall = { enable = true; natEnabled = true; };
    ssh = { enable = true; port = 22; passwordAuth = false; };
    ipv6 = { enable = false; wanMode = "disabled"; };
  });
  # Deliberately public synthetic fixtures, never production credentials.
  adminPassword = pkgs.writeText "managed-wifi-test-admin" "synthetic-admin-password\n";
  stationDHCPHook = pkgs.writeShellScript "wifi-station-dhcp-hook" ''
    set -eu
    case "$1" in
      deconfig) ${pkgs.iproute2}/bin/ip -4 address flush dev "$interface" scope global ;;
      bound|renew)
        prefix=$(${pkgs.busybox}/bin/ipcalc -p "$ip" "$subnet")
        prefix=''${prefix#PREFIX=}
        ${pkgs.iproute2}/bin/ip -4 address flush dev "$interface" scope global
        ${pkgs.iproute2}/bin/ip address add "$ip/$prefix" dev "$interface"
        printf '%s\n' "$ip" > /run/station-dhcp-address
        ;;
    esac
  '';
in {
  name = "managed-wifi";
  requiredFeatures.kvm = false;
  qemu.forceAccel = false;
  globalTimeout = 1800;
  nodes.machine = { lib, ... }: {
    imports = [ self.nixosModules.default ];
    virtualisation = { memorySize = 1536; cores = 2; };
    boot.kernelModules = [ "mac80211_hwsim" ];
    hardware.wirelessRegulatoryDatabase = true;
    boot.extraModprobeConfig = "options mac80211_hwsim radios=2";
    networking = {
      useDHCP = false;
      usePredictableInterfaceNames = false;
      dhcpcd.enable = false;
      firewall.enable = false;
      wireless.enable = lib.mkForce false;
    };
    services.resolved.enable = false;
    # Nixpkgs wpa_cli binds its client socket here even with a custom -p path.
    # No generic wpa_supplicant service is enabled to create it for this fixture.
    systemd.tmpfiles.rules = [
      "d /run/wpa_supplicant 0755 root root -"
      "d /run/wpa_supplicant/client 0700 root root -"
    ];
    environment.systemPackages = with pkgs; [ curl jq iproute2 iputils iw wpa_supplicant busybox nftables ];
    environment.etc."wifi-station-dhcp-hook".source = stationDHCPHook;
    services.nixos-router.backend = {
      enable = true;
      package = self.packages.${pkgs.stdenv.hostPlatform.system}.routerd;
      dev = false;
      managed = {
        enable = true;
        interfaces = [ "wan0" "lan0" ];
        wifiInterface = "wlan0";
        countryCode = "US";
        bootstrapConfigFile = toString bootstrap;
        adminPasswordFile = toString adminPassword;
      };
    };
    # These are only isolated physical-interface substitutes. The setup creates
    # no AP, bridge, address, DHCP server or route: routerd must create those.
    systemd.services.wifi-test-links = {
      before = [ "nixos-router-backend.service" ];
      requiredBy = [ "nixos-router-backend.service" ];
      after = [ "systemd-modules-load.service" ];
      path = [ pkgs.iproute2 pkgs.iw pkgs.systemd pkgs.kmod ];
      serviceConfig = { Type = "oneshot"; RemainAfterExit = true; };
      script = ''
        modprobe mac80211_hwsim radios=2
        udevadm settle
        test -d /sys/class/net/wlan0
        test -d /sys/class/net/wlan1
        ip link add wan0 type dummy
        ip link add lan0 type dummy
        ip netns add station
        iw phy phy1 set netns name station
        ip netns exec station ip link set lo up
        ip netns exec station ip link set wlan1 up
      '';
    };
  };
  testScript = builtins.readFile ./managed-wifi.py;
}
