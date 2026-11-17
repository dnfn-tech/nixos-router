# Software-only WiFi authentication, DHCP and isolated routed-network acceptance. Real RF, MT7927 firmware, throughput and
# regulatory compliance remain separate hardware acceptance requirements.
{ pkgs, self, security ? "wpa2-psk", routed ? false, standard ? "", channelWidthMHz ? 0 }:
let
  bootstrap = pkgs.writeText "managed-wifi-bootstrap.json" (builtins.toJSON {
    system = { hostname = "wifi-router"; timezone = "UTC"; };
    wan = { interface = "wan0"; mode = "static"; static = { addressCidr = "10.99.0.2/24"; gateway = "10.99.0.1"; }; };
    lan = {
      bridgeName = "br-lan"; ipv4Cidr = "192.168.77.1/24"; ports = [ "lan0" ];
      dhcp = { enable = true; rangeStart = "192.168.77.100"; rangeEnd = "192.168.77.110"; leaseMins = 30; };
    };
    dns = { enableDnsmasq = true; domain = "lan"; upstreams = [ "10.99.0.1" ]; };
    wifi = {
      enable = false; bridgeToLan = !routed;
      aps = [{ enable = true; inherit security standard channelWidthMHz; ssid = "managed-wifi-test"; band = if standard == "" then "2g" else "5g"; channel = if standard == "" then 6 else 149; psk = "synthetic-wifi-password"; }];
    } // pkgs.lib.optionalAttrs routed {
      network = {
        ipv4Cidr = "192.168.88.1/24";
        dhcp = { enable = true; rangeStart = "192.168.88.100"; rangeEnd = "192.168.88.110"; leaseMins = 30; };
        allowManagement = false; allowWiredAccess = false;
      };
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
        ${pkgs.iproute2}/bin/ip route replace default via "''${router%% *}" dev "$interface"
        printf '%s\n' "$router" > "/run/test-dhcp-$interface-router"
        printf '%s\n' "$dns" > "/run/test-dhcp-$interface-dns"
        printf '%s\n' "$ip" > "/run/test-dhcp-$interface-address"
        printf '%s\n' "$ip" > /run/station-dhcp-address
        ;;
    esac
  '';
in {
  name = "managed-wifi-${security}" + pkgs.lib.optionalString routed "-routed" + pkgs.lib.optionalString (standard != "") "-${standard}-${toString channelWidthMHz}";
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
    environment.systemPackages = with pkgs; [ curl jq iproute2 iputils iw wpa_supplicant busybox nftables dnsutils python3 ];
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
    systemd.services.nixos-router-hostapd.serviceConfig.ExecStartPre = lib.optional routed (pkgs.writeShellScript "test-ap-fail-once" ''
      if test -f /run/test-ap-fail-once; then
        ${pkgs.coreutils}/bin/rm /run/test-ap-fail-once
        exit 1
      fi
    '');
    # Only the ISP gets fixture addresses/routes and services. The CPE bridge,
    # wired/WiFi gateway addresses, DHCP/DNS, NAT and routes must come from Apply.
    systemd.services.wifi-test-isp-dns = {
      wantedBy = [ "multi-user.target" ];
      requires = [ "wifi-test-links.service" ]; after = [ "wifi-test-links.service" ];
      serviceConfig.ExecStart = "${pkgs.iproute2}/bin/ip netns exec isp ${pkgs.dnsmasq}/bin/dnsmasq --keep-in-foreground --conf-file=/dev/null --no-hosts --no-resolv --bind-interfaces --interface=isp0 --listen-address=10.99.0.1 --address=/uplink.test/203.0.113.1";
    };
    systemd.services.wifi-test-isp-http = {
      wantedBy = [ "multi-user.target" ];
      requires = [ "wifi-test-links.service" ]; after = [ "wifi-test-links.service" ];
      serviceConfig.ExecStart = "${pkgs.iproute2}/bin/ip netns exec isp ${pkgs.python3}/bin/python3 ${pkgs.writeText "wifi-isp-http.py" ''
        from http.server import BaseHTTPRequestHandler, HTTPServer
        class H(BaseHTTPRequestHandler):
            def do_GET(self):
                self.send_response(200)
                self.end_headers()
                self.wfile.write(self.client_address[0].encode())
        HTTPServer(('0.0.0.0',8090),H).serve_forever()
      ''}";
    };
    systemd.services.wifi-test-links = {
      before = [ "nixos-router-executor.service" ];
      requiredBy = [ "nixos-router-executor.service" ];
      after = [ "systemd-modules-load.service" ];
      path = [ pkgs.iproute2 pkgs.iw pkgs.systemd pkgs.kmod ];
      serviceConfig = { Type = "oneshot"; RemainAfterExit = true; };
      script = ''
        modprobe mac80211_hwsim radios=2
        udevadm settle
        test -d /sys/class/net/wlan0
        test -d /sys/class/net/wlan1
        iw reg set US
        ip netns add isp
        ip netns add wired
        ip link add wan0 type veth peer name isp0
        ip link add lan0 type veth peer name wired0
        ip link set isp0 netns isp
        ip link set wired0 netns wired
        ip -n isp link set lo up
        ip -n isp link set isp0 up
        ip -n isp address add 10.99.0.1/24 dev isp0
        ip -n isp address add 203.0.113.1/32 dev lo
        ip -n isp route add 192.168.77.0/24 via 10.99.0.2
        ip -n isp route add 192.168.88.0/24 via 10.99.0.2
        ip -n wired link set lo up
        ip -n wired link set wired0 up
        ip netns add station
        iw phy phy1 set netns name station
        ip netns exec station ip link set lo up
        ip netns exec station ip link set wlan1 up
      '';
    };
  };
  testScript = "RADIO_STANDARD = ${builtins.toJSON standard}\nRADIO_WIDTH = ${toString channelWidthMHz}\nSECURITY = ${builtins.toJSON security}\nROUTED = ${if routed then "True" else "False"}\n" + builtins.readFile ./managed-wifi.py;
}
