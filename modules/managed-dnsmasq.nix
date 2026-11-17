{ pkgs }:
# Only the independent manager uses this build. Its constructed prefixes are
# explicitly owned, finite-lived addresses; deleting one withdraws the prefix.
# Keep deprecated-but-present addresses valid until their kernel deadline.
# Packet-level regression: tests/ipv6-ra-lifetimes.py.
pkgs.dnsmasq.overrideAttrs (old: {
  patches = (old.patches or []) ++ [ ./patches/dnsmasq-owned-prefix-lifetimes.patch ];
})
