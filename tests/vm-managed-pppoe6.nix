# Real PPPoE/IPv6CP and DHCPv6-PD acceptance; all credentials are public fixtures.
{ pkgs, self }:
let
  base = import ./vm-managed-pppoe.nix { inherit pkgs self; };
  keaConfig = pkgs.writeText "ppp6-kea.json" (builtins.toJSON {
    Dhcp6 = {
      interfaces-config = { interfaces = [ "ppp0" ]; service-sockets-require-all = true; service-sockets-max-retries = 20; service-sockets-retry-wait-time = 1000; };
      valid-lifetime = 120; preferred-lifetime = 60; renew-timer = 20; rebind-timer = 40;
      lease-database = { type = "memfile"; persist = true; name = "/var/lib/kea/ppp6-leases.csv"; };
      subnet6 = [{ id = 1; interface = "ppp0"; subnet = "2001:db8:77::/64";
        pools = [{ pool = "2001:db8:77::100-2001:db8:77::200"; }];
        pd-pools = [{ prefix = "2001:db8:100::"; prefix-len = 56; delegated-len = 60; }];
        option-data = [{ name = "dns-servers"; data = "2001:db8:ffff::53"; }];
      }];
    };
  });
  raConfig = pkgs.writeText "ppp6-ra.conf" ''
    interface ppp0 {
      IgnoreIfMissing on;
      AdvSendAdvert on;
      MinRtrAdvInterval 3;
      MaxRtrAdvInterval 5;
      AdvManagedFlag on;
      AdvOtherConfigFlag on;
      AdvLinkMTU 1400;
      prefix 2001:db8:77::/64 {
        AdvOnLink on;
        AdvAutonomous on;
        AdvValidLifetime 300;
        AdvPreferredLifetime 120;
      };
    };
  '';
  upHook = pkgs.writeShellScript "test-ppp6-up" ''
    set -eu
    test "$1" = ppp0
    # Kea's Linux interface discovery reads IFA_ADDRESS (the peer on PPP)
    # instead of IFA_LOCAL. Keep the negotiated ISP local address without a
    # peer attribute so the real server binds its own address, not the CPE's.
    # A link-local /64 route still reaches the negotiated peer. CPE addresses
    # and all CPE protocol callbacks remain entirely PPP/odhcp6c controlled.
    ${pkgs.iproute2}/bin/ip -6 address del "$4" peer "$5/128" dev ppp0
    ${pkgs.iproute2}/bin/ip -6 address add "$4/64" dev ppp0 nodad
    ${pkgs.iproute2}/bin/ip -6 address replace 2001:db8:77::1/64 dev ppp0
    ${pkgs.iproute2}/bin/ip -6 route replace 2001:db8:100::/56 dev ppp0
    ${pkgs.systemd}/bin/systemctl --no-block restart test-ppp6-kea.service test-ppp6-ra.service
  '';
  downHook = pkgs.writeShellScript "test-ppp6-down" ''
    ${pkgs.systemd}/bin/systemctl --no-block stop test-ppp6-kea.service test-ppp6-ra.service
  '';
  options = pkgs.writeText "test-ppp6-server-options" ''
    require-pap
    refuse-chap
    refuse-mschap
    refuse-mschap-v2
    refuse-eap
    auth
    name test-isp
    ms-dns 10.0.0.1
    mtu 1492
    mru 1400
    unit 0
    +ipv6
    ipv6 ::1,::2
    ipv6cp-accept-remote
    ipv6-up-script ${upHook}
    ipv6-down-script ${downHook}
    lcp-echo-interval 5
    lcp-echo-failure 3
    hide-password
  '';
  # rp-pppoe appends MTU/MRU arguments. Override its receive MRU last so the
  # CPE must negotiate a real 1400-byte transmit link instead of assuming 1492.
  serverPPP = pkgs.writeShellScript "test-ppp6-pppd" ''
    exec ${pkgs.ppp}/sbin/pppd "$@" mru 1400
  '';
in base // {
  name = "vm-managed-pppoe6";
  nodes = {
    inherit (base.nodes) router client;
    upstream = { lib, ... }: {
      imports = [ base.nodes.upstream ];
      networking.interfaces.lo.ipv6.addresses = [{ address = "2001:db8:ffff::53"; prefixLength = 128; }];
      systemd.services.network-addresses-lo.wantedBy = [ "multi-user.target" ];
      boot.kernel.sysctl = { "net.ipv6.conf.all.forwarding" = 1; "net.ipv4.ip_forward" = 1; };
      services.dnsmasq.settings = {
        interface = lib.mkForce [ "eth1" "lo" ];
        listen-address = lib.mkForce [ "10.0.0.1" "2001:db8:ffff::53" ];
        address = lib.mkAfter [ "/v6.test.isp/2001:db8:feed::2" ];
      };
      systemd.services.test-pppoe-server.serviceConfig.ExecStart = lib.mkForce "${pkgs.rp-pppoe}/sbin/pppoe-server -F -I eth1 -L 10.77.0.1 -R 10.77.0.10 -N 1 -O ${options} -q ${serverPPP} -Q ${pkgs.rp-pppoe}/sbin/pppoe";
      systemd.services.test-ppp6-kea.serviceConfig = {
        ExecStart = "${lib.getExe' pkgs.kea "kea-dhcp6"} -c ${keaConfig}";
        StateDirectory = "kea";
        RuntimeDirectory = "kea";
        Restart = "on-failure"; RestartSec = 1;
      };
      systemd.services.test-ppp6-ra.serviceConfig = {
        ExecStart = "${pkgs.radvd}/bin/radvd -n -C ${raConfig} -p /run/test-ppp6-ra.pid";
        Restart = "on-failure"; RestartSec = 1;
      };
    };
  };
  testScript = builtins.readFile ./managed-pppoe6.py;
}
