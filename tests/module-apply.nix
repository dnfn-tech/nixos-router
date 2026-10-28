# nix eval --impure --json --file tests/module-apply.nix
let
  flake = builtins.getFlake (toString ../.);
  nixpkgs = flake.inputs.nixpkgs;
  lib = nixpkgs.lib;
  evaluate = enabled: extra:
    (import (nixpkgs + "/nixos/lib/eval-config.nix") {
      system = builtins.currentSystem;
      modules = [
        ../modules/default.nix
        ({ pkgs, ... }: {
          services.nixos-router.backend = {
            enable = true;
            package = pkgs.hello;
            applyReload = enabled;
            consumeGenerated = enabled;
            privilegedApply = enabled;
            applyTrafficControl = enabled;
          };
          networking.dhcpcd.enable = lib.mkDefault false;
          # The include must compose with operator-provided dnsmasq settings.
          services.dnsmasq.settings.conf-file = [ "/etc/custom-dnsmasq.conf" ];
          system.stateVersion = "26.05";
          boot.loader.grub.enable = false;
          fileSystems."/" = { device = "none"; fsType = "tmpfs"; };
        })
        extra
      ];
    }).config;
  check = enabled:
    let
      config = evaluate enabled {};
      service = config.systemd.services.nixos-router-backend;
      expected = lib.boolToString enabled;
      includes = config.services.dnsmasq.settings.conf-file;
    in
    assert service.environment.NIXOS_ROUTER_CONSUME_GENERATED == expected;
    assert service.environment.NIXOS_ROUTER_PRIVILEGED_APPLY == expected;
    assert service.environment.NIXOS_ROUTER_APPLY_TRAFFIC_CONTROL == expected;
    assert service.serviceConfig.DynamicUser == !enabled;
    assert lib.hasInfix "--apply-reload" service.serviceConfig.ExecStart == enabled;
    assert builtins.elem "/var/lib/nixos-router/generated/dnsmasq.conf.fragment" includes == enabled;
    assert builtins.elem "/etc/custom-dnsmasq.conf" includes;
    assert !enabled || service.serviceConfig.User == "root";
    assert !enabled || lib.any (pkg: lib.getName pkg == "nftables") service.path;
    assert !enabled || lib.any (pkg: lib.getName pkg == "iproute2") service.path;
    assert lib.assertMsg (lib.all (entry: entry.assertion) config.assertions)
      (lib.concatMapStringsSep "\n" (entry: entry.message)
        (lib.filter (entry: !entry.assertion) config.assertions));
    true;
  conflicts = [
    { networking.networkmanager.enable = true; }
    { systemd.network.enable = true; }
    { networking.dhcpcd.enable = true; }
    { services.connman.enable = true; }
  ];
  routerFailures = config: lib.filter
    (entry: !entry.assertion && lib.hasPrefix "nixos-router runtime apply" entry.message)
    config.assertions;
  checkConflict = extra:
    assert builtins.length (routerFailures (evaluate true extra)) > 0;
    assert routerFailures (evaluate false extra) == [];
    true;
in {
  defaults = check false;
  optedIn = check true;
  exclusiveManagers = map checkConflict conflicts;
}
