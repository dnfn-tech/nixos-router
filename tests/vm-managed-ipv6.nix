{ pkgs, self }:
let
  base = import ./vm-managed-router.nix { inherit pkgs self; };
in base // {
  name = "vm-managed-ipv6";
  nodes = {
    inherit (base.nodes) client;
    router = { ... }: {
      imports = [ base.nodes.router ];
      environment.systemPackages = [ self.packages.x86_64-linux.routerd ];
      services.nixos-router.backend.managed.wanUDPPorts = [ 9993 ];
      systemd.services.test-host-udp = {
        wantedBy = [ "multi-user.target" ];
        serviceConfig.ExecStart = "${pkgs.python3}/bin/python3 ${pkgs.writeText "host-udp-echo.py" (builtins.readFile ./host-udp-echo.py)}";
      };
    };
    upstream = { lib, ... }: {
      imports = [ base.nodes.upstream ];
      networking.interfaces.eth1.ipv6.addresses = [{ address = "2001:db8:1::1"; prefixLength = 64; }];
      networking.interfaces.lo.ipv6.addresses = [{ address = "2001:db8:ffff::1"; prefixLength = 128; }];
      # Scripted networking gives loopback no device-triggered wantedBy.
      systemd.services.network-addresses-lo.wantedBy = [ "multi-user.target" ];
      boot.kernel.sysctl."net.ipv6.conf.all.forwarding" = 1;
      services.dnsmasq.settings = {
        listen-address = lib.mkForce [ "10.0.0.1" "2001:db8:1::1" ];
        address = lib.mkAfter [ "/v6.test.isp/2001:db8:ffff::1" ];
      };
      services.radvd = {
        enable = true;
        config = ''
          interface eth1 {
            AdvSendAdvert on;
            MinRtrAdvInterval 3;
            MaxRtrAdvInterval 5;
            AdvManagedFlag on;
            AdvOtherConfigFlag on;
            AdvLinkMTU 1400;
            prefix 2001:db8:1::/64 {
              AdvOnLink on;
              AdvAutonomous on;
              AdvValidLifetime 300;
              AdvPreferredLifetime 120;
            };
          };
        '';
      };
      services.kea.dhcp6 = {
        enable = true;
        settings = {
          interfaces-config = {
            interfaces = [ "eth1" ];
            # Link-local DAD can still be in progress when the service starts.
            service-sockets-require-all = true;
            service-sockets-max-retries = 20;
            service-sockets-retry-wait-time = 1000;
          };
          valid-lifetime = 120;
          preferred-lifetime = 60;
          renew-timer = 20;
          rebind-timer = 40;
          lease-database = { type = "memfile"; persist = true; name = "/var/lib/kea/dhcp6.leases"; };
          subnet6 = [{
            id = 1;
            interface = "eth1";
            subnet = "2001:db8:1::/64";
            pools = [{ pool = "2001:db8:1::100-2001:db8:1::200"; }];
            pd-pools = [{ prefix = "2001:db8:100::"; prefix-len = 56; delegated-len = 60; }];
            option-data = [{ name = "dns-servers"; data = "2001:db8:1::1"; }];
          }];
        };
      };
    };
  };
  testScript = builtins.readFile ./managed-ipv6.py;
}
