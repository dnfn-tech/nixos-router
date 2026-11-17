{ pkgs, self }:
let
  bootstrap = pkgs.writeText "privsep-test-bootstrap.json" (builtins.toJSON {
    system = { hostname = "router"; timezone = "UTC"; };
    wan = { interface = "wan0"; mode = "static"; static = { addressCidr = "192.0.2.2/24"; gateway = "192.0.2.1"; dns = [ "192.0.2.1" ]; }; };
    lan = { bridgeName = "br-lan"; ipv4Cidr = "192.168.50.1/24"; ports = [ "lan0" ]; dhcp = { enable = true; rangeStart = "192.168.50.100"; rangeEnd = "192.168.50.150"; leaseMins = 60; }; };
    dns = { enableDnsmasq = true; upstreams = [ "192.0.2.1" ]; domain = "lan"; };
    wifi = { enable = false; bridgeToLan = true; aps = []; };
    firewall = { enable = true; natEnabled = true; };
  });
  newPassword = pkgs.writeText "unused-new-password" "not-the-existing-password";
in {
  name = "vm-privsep";
  requiredFeatures.kvm = false;
  qemu.forceAccel = false;
  globalTimeout = 1800;
  nodes.machine = { ... }: {
    imports = [ self.nixosModules.default ];
    virtualisation.memorySize = 768;
    virtualisation.cores = 2;
    networking.firewall.enable = false;
    networking.useDHCP = false;
    networking.dhcpcd.enable = false;
    services.resolved.enable = false;
    environment.systemPackages = with pkgs; [ curl iproute2 python3 util-linux ];
    services.nixos-router.backend = {
      enable = true;
      package = self.packages.x86_64-linux.routerd;
      address = "127.0.0.1:8080";
      managed = { enable = true; interfaces = [ "wan0" "lan0" ]; bootstrapConfigFile = toString bootstrap; adminPasswordFile = toString newPassword; };
    };
    systemd.services.legacy-router-fixture = {
      before = [ "nixos-router-state-init.service" ];
      requiredBy = [ "nixos-router-state-init.service" ];
      after = [ "local-fs.target" ];
      path = [ pkgs.iproute2 pkgs.curl pkgs.coreutils ];
      serviceConfig = { Type = "oneshot"; RemainAfterExit = true; UMask = "0077"; };
      script = ''
        ip link add wan0 type dummy
        ip link add lan0 type dummy
        install -d -m 0700 /var/lib/nixos-router
        if ! test -e /var/lib/nixos-router/api-layout.json; then
          install -m 0600 ${bootstrap} /var/lib/nixos-router/config.json
          install -d -m 0700 /var/lib/nixos-router/managed
          install -m 0600 ${bootstrap} /var/lib/nixos-router/managed/last-good.json
          ${pkgs.python3}/bin/python3 -c 'import json; p="/var/lib/nixos-router/config.json"; c=json.load(open(p)); c["system"]["hostname"]="legacy-draft"; json.dump(c,open(p,"w"))'
          NIXOS_ROUTER_ADMIN_PASSWORD=existing-test-password ${self.packages.x86_64-linux.routerd}/bin/routerd --state-dir /var/lib/nixos-router --addr 127.0.0.1:18081 &
          old_pid=$!
          trap 'kill "$old_pid" 2>/dev/null || true' EXIT
          for i in $(seq 1 60); do
            if curl -fsS http://127.0.0.1:18081/api/v1/health >/dev/null; then break; fi
            sleep 0.2
          done
          curl -fsS -c /run/legacy-cookie -H 'Content-Type: application/json' -d '{"username":"admin","password":"existing-test-password"}' http://127.0.0.1:18081/api/v1/session
          kill "$old_pid"
          wait "$old_pid" || true
          trap - EXIT
          ${pkgs.python3}/bin/python3 - <<'PY'
        import sqlite3
        with sqlite3.connect('/var/lib/nixos-router/state.db') as db:
            db.execute("INSERT INTO jobs (id, kind, status, created_at, updated_at, payload) VALUES ('legacy-running', 'apply', 'running', 1, 1, '{}')")
        PY
        fi
      '';
    };
  };
  testScript = builtins.readFile ./privsep.py;
}
