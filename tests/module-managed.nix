# nix eval --impure --json --file tests/module-managed.nix
let
  flake = builtins.getFlake (toString ../.);
  nixpkgs = flake.inputs.nixpkgs;
  lib = nixpkgs.lib;
  evaluate = extra: (import (nixpkgs + "/nixos/lib/eval-config.nix") {
    system = builtins.currentSystem;
    modules = [ ../modules/default.nix ({pkgs,...}: {
      services.nixos-router.backend = {
        enable = true; package = pkgs.hello;
        managed = { enable = true; interfaces = [ "eth1" "eth2" ]; bootstrapConfigFile = "/run/router-bootstrap.json"; };
      };
      networking.useDHCP = false;
      networking.dhcpcd.enable = false;
      networking.firewall.enable = lib.mkDefault false;
      boot.loader.grub.enable = false;
      fileSystems."/" = {device="none";fsType="tmpfs";};
      system.stateVersion = "26.05";
    }) extra ];
  }).config;
  cfg = evaluate {};
  failures = c: lib.filter (a: !a.assertion) c.assertions;
in {
  integrated = assert lib.assertMsg (failures cfg == []) (lib.concatMapStringsSep "\n" (a:a.message) (failures cfg));
    assert cfg.services.nixos-router.backend.applyReload;
    assert cfg.services.nixos-router.backend.privilegedApply;
    assert !cfg.services.nixos-router.backend.consumeGenerated;
    assert cfg.systemd.services.nixos-router-backend.environment.NIXOS_ROUTER_MANAGED == "1";
    assert cfg.systemd.services.nixos-router-backend.environment.NIXOS_ROUTER_MANAGED_SSH_PORTS == "22";
    assert cfg.systemd.services.nixos-router-dnsmasq.serviceConfig.Type == "forking";
    assert builtins.hasAttr "nixos-router-wan" cfg.systemd.services;
    assert builtins.hasAttr "nixos-router-dnsmasq" cfg.systemd.services;
    assert builtins.hasAttr "nixos-router-hostapd" cfg.systemd.services;
    assert cfg.networking.firewall.allowedTCPPorts == [];
    true;
  hostSSHPorts = assert (evaluate { services.openssh.ports = [ 22 2223 ]; }).systemd.services.nixos-router-backend.environment.NIXOS_ROUTER_MANAGED_SSH_PORTS == "22,2223"; true;
  hostSSHListenPorts = let
    sshCfg = evaluate { services.openssh = {
      ports = [ 22 2223 ];
      listenAddresses = [
        { addr = "0.0.0.0"; }
        { addr = "::"; }
        { addr = "0.0.0.0"; port = 2224; }
        { addr = "[::]"; port = 2224; }
        { addr = "127.0.0.1"; port = 22; }
      ];
    }; };
  in assert sshCfg.systemd.services.nixos-router-backend.environment.NIXOS_ROUTER_MANAGED_SSH_PORTS == "22,2223,2224"; true;
  conflictRejected = map (extra: assert failures (evaluate extra) != []; true) [
    {networking.networkmanager.enable = true;}
    {systemd.network.enable = true;}
    {services.kea.dhcp4.enable = true;}
    {services.dnsmasq.enable = true;}
    {services.hostapd.enable = true;}
    {networking.firewall.enable = true;}
    {services.nixos-router.backend.dev = true;}
    {services.nixos-router.backend.managed.interfaces = lib.mkForce [];}
  ];
}
