#!/usr/bin/env bash
set -euo pipefail
#
# Compute vendorHash for pkgs.buildGoModule (routerd) by running `nix build`
# and extracting the "got: sha256-..." hint. With --apply, this script will
# update flake.nix in-place to replace lib.fakeSha256 (or an old hash) with
# the computed value.
#
# Usage:
#   ./scripts/compute-vendor-hash.sh           # print detected hash
#   ./scripts/compute-vendor-hash.sh --apply   # update flake.nix in-place
#
# Requirements: Nix with flakes enabled. On Linux hosts without a nix-daemon,
# prefer single-user install: `sh <(curl -L https://nixos.org/nix/install) --no-daemon`
#

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APPLY=0
if [[ "${1:-}" == "--apply" ]]; then
  APPLY=1
fi

set +e
OUT="$(cd "$ROOT" && nix build .#routerd -L 2>&1)"
RC=$?
set -e

if [[ $RC -eq 0 ]]; then
  echo "Build succeeded; a vendorHash is already correct."
  exit 0
fi

HASH="$(printf '%s\n' "$OUT" | sed -nE 's/.*got: *((sha256|sha-256)[-a-zA-Z0-9+/=]+).*/\\1/p' | tail -n1)"
if [[ -z "$HASH" ]]; then
  echo "Could not detect vendorHash from nix output."
  echo "Full error output follows:"
  printf '%s\n' "$OUT"
  exit 2
fi

echo "Detected vendorHash: $HASH"
if [[ $APPLY -eq 1 ]]; then
  FILE="$ROOT/flake.nix"
  if [[ ! -f "$FILE" ]]; then
    echo "flake.nix not found at $FILE"
    exit 3
  fi
  # Replace lib.fakeSha256 or an existing sha256-... with the new HASH
  TMP="$FILE.tmp.$$"
  sed -E "s#(vendorHash\\s*=)\\s*(lib\\.fakeSha256|\"sha[0-9-]+[A-Za-z0-9+/=]+\")#\\1 \"$HASH\"#g" "$FILE" > "$TMP"
  mv "$TMP" "$FILE"
  echo "Updated $FILE with vendorHash = \"$HASH\""
fi

