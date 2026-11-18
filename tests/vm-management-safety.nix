{ pkgs, self }:
let
  bootstrap = pkgs.writeText "management-safety-bootstrap.json" (builtins.toJSON {
    system = { hostname = "router"; timezone = "UTC"; };
    wan = { interface = "eth1"; mode = "static"; static = { addressCidr = "10.0.0.2/24"; gateway = "10.0.0.1"; dns = [ "10.0.0.1" ]; }; };
    lan = { bridgeName = "br-lan"; ipv4Cidr = "192.168.50.1/24"; ports = [ "eth2" ]; dhcp = { enable = true; rangeStart = "192.168.50.100"; rangeEnd = "192.168.50.150"; leaseMins = 60; }; };
    dns = { enableDnsmasq = true; upstreams = [ "10.0.0.1" ]; domain = "lan"; };
    wifi = { enable = false; bridgeToLan = true; aps = []; };
    firewall = { enable = true; natEnabled = true; };
  });
  secret = pkgs.writeText "management-safety-synthetic-password" "test-password-123";
  common = { ... }: {
    virtualisation.memorySize = 640;
    virtualisation.cores = 2;
    networking.firewall.enable = false;
    networking.useDHCP = false;
    networking.dhcpcd.enable = false;
    services.resolved.enable = false;
    environment.systemPackages = with pkgs; [ curl iproute2 iputils nftables python3 netcat-openbsd ];
  };
in {
  name = "vm-management-safety";
  requiredFeatures.kvm = false;
  qemu.forceAccel = false;
  globalTimeout = 2400;
  nodes = {
    router = { ... }: {
      imports = [ common self.nixosModules.default ];
      virtualisation.interfaces = {
        eth1 = { vlan = 1; assignIP = false; };
        eth2 = { vlan = 2; assignIP = false; };
        eth3 = { vlan = 3; assignIP = false; };
      };
      networking.interfaces.eth3 = {
        ipv4.addresses = [{ address = "10.66.0.1"; prefixLength = 24; }];
        ipv6.addresses = [{ address = "fd66::1"; prefixLength = 64; }];
      };
      services.openssh.enable = true;
      services.nixos-router.backend = {
        enable = true;
        package = self.packages.x86_64-linux.routerd;
        # Deliberately fixed original LAN address: managed runtime must normalize it.
        address = "192.168.50.1:8080";
        managed = {
          enable = true;
          interfaces = [ "eth1" "eth2" ];
          bootstrapConfigFile = toString bootstrap;
          adminPasswordFile = toString secret;
          confirmationTimeoutSeconds = 60;
          managementAccess = [{ interface = "eth3"; sources = [ "10.66.0.2/32" "fd66::2/128" ]; }];
        };
      };
    };
    upstream = { ... }: {
      imports = [ common ];
      virtualisation.interfaces.eth1 = { vlan = 1; assignIP = false; };
      networking.interfaces.eth1 = {
        ipv4.addresses = [{ address = "10.0.0.1"; prefixLength = 24; }];
        ipv6.addresses = [{ address = "fd01::1"; prefixLength = 64; }];
      };
    };
    client = { ... }: {
      imports = [ common ];
      virtualisation.interfaces.eth1 = { vlan = 2; assignIP = false; };
      networking.interfaces.eth1.ipv4.addresses = [{ address = "192.168.50.2"; prefixLength = 24; }];
    };
    overlay = { ... }: {
      imports = [ common ];
      virtualisation.interfaces.eth1 = { vlan = 3; assignIP = false; };
      networking.interfaces.eth1 = {
        ipv4.addresses = [{ address = "10.66.0.2"; prefixLength = 24; }];
        ipv6.addresses = [{ address = "fd66::2"; prefixLength = 64; }];
      };
    };
  };
  testScript = builtins.readFile ./management-safety.py;
}
