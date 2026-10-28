{ pkgs, self }:
let
  bootstrap = pkgs.writeText "managed-test-bootstrap.json" (builtins.toJSON {
    system = { hostname = "router"; timezone = "UTC"; };
    wan = { interface = "eth1"; mode = "dhcp"; };
    lan = { bridgeName = "br-lan"; ipv4Cidr = "192.168.50.1/24"; ports = [ "eth2" ]; dhcp = { enable = true; rangeStart = "192.168.50.100"; rangeEnd = "192.168.50.150"; leaseMins = 60; }; };
    dns = { enableDnsmasq = true; upstreams = [ "10.0.0.1" ]; domain = "lan"; };
    wifi = { enable = false; bridgeToLan = true; aps = []; };
    firewall = { enable = true; natEnabled = true; };
    ssh = { enable = true; port = 2222; passwordAuth = false; };
  });
  secret = pkgs.writeText "synthetic-test-password" "test-password-123";
  common = { ... }: {
    virtualisation.memorySize = 512;
    virtualisation.cores = 1;
    networking.firewall.enable = false;
    networking.useDHCP = false;
    services.resolved.enable = false;
    environment.systemPackages = with pkgs; [ curl iproute2 iputils dnsutils dnsmasq nftables netcat-openbsd python3 ];
  };
  clientNode = { ... }: {
    imports = [ common ];
    virtualisation.interfaces.eth1 = { vlan = 2; assignIP = false; };
    networking.useNetworkd = true;
    networking.dhcpcd.enable = false;
    networking.interfaces.eth1.useDHCP = true;
  };
in {
  name = "vm-managed-router";
  requiredFeatures.kvm = false;
  qemu.forceAccel = false;
  globalTimeout = 3600;
  nodes = {
    upstream = { ... }: {
      imports = [ common ];
      virtualisation.interfaces.eth1 = { vlan = 1; assignIP = false; };
      networking.interfaces.eth1.ipv4.addresses = [{ address = "10.0.0.1"; prefixLength = 24; }];
      services.dnsmasq = { enable = true; settings = {
        interface = "eth1"; listen-address = "10.0.0.1"; bind-interfaces = true; dhcp-authoritative = true;
        dhcp-range = "10.0.0.100,10.0.0.200,1h";
        no-resolv = true; address = [ "/test.isp/10.0.0.1" ];
      }; };
    };
    router = { ... }: {
      imports = [ common self.nixosModules.default ];
      virtualisation.interfaces.eth1 = { vlan = 1; assignIP = false; };
      virtualisation.interfaces.eth2 = { vlan = 2; assignIP = false; };
      virtualisation.interfaces.eth3 = { vlan = 3; assignIP = false; };
      services.openssh = {
        enable = true;
        ports = [ 22 2223 ];
        # Null ports retain both configured ports; explicit ports add listeners
        # that must also be covered by routerd's management-plane firewall.
        listenAddresses = [
          { addr = "0.0.0.0"; }
          { addr = "::"; }
          { addr = "0.0.0.0"; port = 2224; }
          { addr = "[::]"; port = 2224; }
        ];
        settings.PasswordAuthentication = false;
      };
      networking.dhcpcd.enable = false;
      services.nixos-router.backend = {
        enable = true;
        package = self.packages.x86_64-linux.routerd;
        address = "[::]:8080";
        managed = { enable = true; interfaces = [ "eth1" "eth2" "eth3" ]; bootstrapConfigFile = toString bootstrap; adminPasswordFile = toString secret; };
      };
    };
    client = clientNode;
    observer = clientNode;
  };
  testScript = builtins.readFile ./managed-router.py;
}
