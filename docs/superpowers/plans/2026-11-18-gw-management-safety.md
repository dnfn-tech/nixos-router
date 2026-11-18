# Managed management safety implementation plan

Goal: close the three explicitly assigned takeover risks on main 1a1b5ab.
Architecture: trusted deployment policy authorizes exact management interface/source pairs; executor owns durable candidate state and confirmation; managed HTTP uses a wildcard listener behind the executor firewall.
Tech stack: existing Go, nftables, NixOS module and QEMU tests. No new dependencies.

Constraints: change only nixos-router; never operate physical gw or host networking; preserve main worktree changes; no nixos-home/pin edits.

Design contract:
- Additional management grants are bounded to 32 exact interface names and 32 non-default source prefixes each. They authorize only configured SSH/Web ports. Owned WAN/PPP, LAN members and WiFi cannot be nominated as external management interfaces. No wildcard names or universal source prefix. Default empty. Rejection precedes network mutation. Established flows cannot bypass management revocation.
- Deployment confirmation timeout is 120 seconds by default, bounded 10..600. A trusted bootstrap establishes the initial recovery baseline; bootstrap is not remote reachability proof. User Apply becomes a durable candidate; last-good stays byte-for-byte unchanged until authenticated explicit confirmation. One pending candidate; identifiers are random and tied to a config revision. No implicit browser confirmation.
- Executor owns timeout independent of API/client. Crash during activation/confirmation or unconfirmed reboot restores journaled previous config before serving RPC. Terminal confirmed/rolled-back status is retained for idempotence; old IDs fail after replacement. Expired confirmation never promotes. Rollback failure remains journaled, clears observed availability and blocks new applies; restart retries recovery.
- API exposes candidate GET and confirm POST, both behind existing authentication/CSRF boundary; Unix RPC never accepts policy from configuration. UI displays confirmation and deadline and explicitly confirms from the new reachable path.
- Managed mode normalizes configured HTTP bind host to wildcard (same port) after executor recovery; startup cannot bind before firewall restored. NixOS firewall must remain disabled as already asserted; no WAN access granted. Document exact integration changes.

Alternatives: reject all LAN changes under fixed listen (leaves overlay-only access unresolved); rebind to changing addresses (more lifecycle races); wildcard plus mandatory manager firewall chosen. Confirmation in API rejected because API can restart/disconnect.

Tasks:
- [x] Write failing managed tests for bounded grants, last-good preservation, confirm/expiry/restart/failure; implement policy and journal lifecycle.
- [x] Test and wire trusted CLI/Nix policy, RPC/API state and confirmation, explicit UI confirmation, stable managed listener.
- [x] Add isolated multi-node VM tests with management overlay surrogate, unauthorized source and WAN probes, address changes and timeout/restarts. Run focused/full/race/module/browser checks.
- [ ] Review diff, commit fixed candidate and report SHA/evidence/limits to assigned nixos-home agent for independent verification.
