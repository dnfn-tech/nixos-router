{ pkgs, self }:
let base = import ./vm-managed-router.nix { inherit pkgs self; };
in base // {
  name = "vm-managed-adblock";
  nodes = base.nodes // {
    upstream = { lib, ... }: {
      imports = [ base.nodes.upstream ];
      services.dnsmasq.settings.address = lib.mkAfter [
        "/ads.example/10.0.0.9" "/ads.example/2001:db8::9"
        "/second.example/10.0.0.10" "/second.example/2001:db8::10"
      ];
    };
  };
  testScript = builtins.readFile ./managed-adblock.py;
}
