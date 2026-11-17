{ pkgs, self }:
let
  base = import ./vm-managed-router.nix { inherit pkgs self; };
  serverOptions = pkgs.writeText "synthetic-pppoe-server-options" ''
    require-pap
    refuse-chap
    refuse-mschap
    refuse-mschap-v2
    refuse-eap
    auth
    name test-isp
    ms-dns 10.0.0.1
    mtu 1492
    mru 1492
    lcp-echo-interval 5
    lcp-echo-failure 3
    hide-password
  '';
in {
  name = "vm-managed-pppoe";
  inherit (base) requiredFeatures qemu globalTimeout;
  nodes = {
    inherit (base.nodes) router client;
    upstream = { ... }: {
      imports = [ base.nodes.upstream ];
      boot.kernelModules = [ "ppp_generic" "pppoe" ];
      # Synthetic fixture only; real deployment secrets never enter the store.
      environment.etc."ppp/pap-secrets" = {
        mode = "0600";
        text = ''"router-test" "test-isp" "ppp \"quoted\" \\ # secret" *'';
      };
      systemd.services.test-pppoe-server = {
        wantedBy = [ "multi-user.target" ];
        after = [ "network.target" ];
        serviceConfig = {
          Type = "simple";
          ExecStartPre = "${pkgs.iproute2}/bin/ip link set eth1 up";
          ExecStart = "${pkgs.rp-pppoe}/sbin/pppoe-server -F -I eth1 -L 10.77.0.1 -R 10.77.0.10 -N 8 -O ${serverOptions} -q ${pkgs.ppp}/sbin/pppd -Q ${pkgs.rp-pppoe}/sbin/pppoe";
          KillMode = "control-group";
          Restart = "on-failure";
        };
      };
    };
  };
  testScript = builtins.readFile ./managed-pppoe.py;
}
