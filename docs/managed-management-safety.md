# Managed management access and candidate recovery

This contract applies to managed mode. Generate-only installations do not take
ownership of network configuration. This change does not deploy a gateway.

## Deployment policy

Additional management ingress is explicitly configured in the integrating NixOS
configuration, for example (replace synthetic names and prefixes):

```nix
services.nixos-router.backend.managed = {
  managementAccess = [{
    interface = "zt-example";
    sources = [ "10.66.0.2/32" "fd66::2/128" ];
  }];
  confirmationTimeoutSeconds = 120;
};
```

A grant matches **both** an exact ingress interface and a source prefix. There
are at most 32 grants and 32 canonical, non-default prefixes per grant. The
interface is externally configured, remains outside router ownership, and must
not be an owned WAN/LAN member, AP, bridge, loopback or PPP egress. Wildcard names
are rejected. The grant permits only the configured HTTP management port and
system-declared SSH ports; it does not grant DNS, forwarding, or interface
ownership. Default grants are empty. Overlay transport UDP ports remain a
separate policy (`wanUDPPorts`); enabling UDP transport does not authorize tunnel
management. Source addresses alone do not establish an overlay identity; the
integrating system must secure its overlay membership and routing.

Management gating is mandatory even when the optional firewall setting is off,
precedes established-connection handling, and covers IPv4 and IPv6. WAN ingress
to either WAN or LAN destination addresses remains denied. NixOS OpenSSH still
owns listeners and authentication. This repository does not configure ZeroTier.

In managed mode routerd normalizes the configured HTTP address to a wildcard
host, retaining its numeric port. It serves only after executor startup has
installed the mandatory management firewall. The API also checks that its port matches the executor-reported protected port; mismatches fail startup. The host portion of `address` no
longer pins managed HTTP to an obsolete LAN IP. This applies to loopback binds
too: choose managed policy deliberately. Generate-only binds are unchanged.
An early NixOS guard blocks management ingress except loopback before network
setup and system SSH startup. The executor installs its full policy before
bringing owned links up. Failed initial activation retains a minimal management
guard instead of deleting protection.
The integrating config must not add another accepting firewall chain or remove
the manager's table; existing module ownership assertions continue to apply.

## Candidate contract

Trusted initial bootstrap is the first recovery baseline. It is not evidence
that remote access works: first takeover still requires an independent console,
an old NixOS generation, correct drivers/secrets, and explicit resolution of
competing network managers. Nothing automatically stops another manager.

Each subsequent user Apply activates one candidate, returns an activation job
and a random confirmation identifier, and starts an executor-owned watchdog.
`last-good.json` stays byte-for-byte unchanged. There is one unsettled candidate;
another Apply is rejected until confirmation or recovery finishes. Job success
means activation completed, not that connectivity was confirmed. `stage` is
`awaiting-confirmation`; `activationStage` retains lease/session observations.

The authenticated browser shows a banner with the deadline and an explicit
confirmation button. It never confirms automatically. After LAN renumbering,
connect to the new address, log in if needed, check required management and
forwarding paths, then confirm. Refresh/login discovers the candidate through
`GET /api/v1/apply/candidate`; no browser storage or original client is required.
An automation can explicitly `POST /api/v1/apply/confirm` with only
`{"id":"<48 hexadecimal characters>"}` after its own connectivity checks.
These endpoints use the existing session and same-origin checks. They do not
accept management policy or extend deadlines. Confirmation proves an operator's
choice; it does not run an Internet probe or prove every required path works.

The timeout defaults to 120 seconds, configurable from 10 through 600 seconds.
The window begins after candidate activation. Client disconnect and API restart
cannot cancel or extend the executor watchdog. An expired candidate cannot be
promoted; recovery uses its journaled baseline, not the current saved draft.
Saved drafts are retained for inspection and are not automatically replayed.

## Restart and failure behavior

The root-private `managed/candidate.json` journal is persisted before mutation.
It records the candidate, original recovery document, identifier, revision,
deadline, and state. Neither the API account nor browser can edit this journal.

| Event | Contract |
| --- | --- |
| API dies or is stopped | Executor continues timeout; API later queries current candidate. |
| Executor/system restarts with an unsettled record | Restore the journaled previous config before RPC readiness, regardless of remaining time. No candidate replay. |
| Crash during activation | Previous config is restored; observed bridge membership and separately tracked resources are reconciled. |
| Crash during confirmation | A durable `confirming` record conservatively restores the original previous document, even if last-good rename already occurred. |
| Confirmation completed durably | Last-good becomes the candidate; restart restores that confirmed config. |
| Same confirmed ID submitted again | Idempotent success while it remains the latest record. |
| Old ID after another candidate, expired or rolled-back ID | Rejected; never promotes another revision. |
| Timeout | Restore exact previous config/document; terminal state `rolled-back`. |
| Recovery fails | Persist `rollback-failed`, report applied configuration unavailable, reject further Apply/confirm. Use independent console; a corrected executor restart retries recovery. |
| Corrupt or untrusted state | Fail startup/readiness; do not guess a recovery config. |

Runtime timeout recovery is bounded by a separate three-minute operation context;
network-service stop time can consume that budget. Failure is not reported as
success. This mechanism cannot recover from broken hardware, a killed executor
that is never restarted, missing/corrupt storage, or changed external network
infrastructure. The system supervisor must restart the executor. Independent
console and the previous NixOS generation remain required for hardware takeover.

The VM management interface is an isolated routed-interface surrogate. Its
IPv4/IPv6 SSH/Web probes exercise kernel filtering and listener behavior, not
actual ZeroTier transport, membership authentication, or physical gw hardware.
