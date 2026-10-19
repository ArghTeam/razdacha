# 0023. Publish network policy as a guarded generation

Date: 2026-10-19 · Status: accepted

## Context

Plain list content is compiled into sing-box JSON but also supplies nft subnet
interception. Independent refreshes can expose selected traffic through direct routing.
The database includes saved edits which must remain pending until explicit Apply.
Neither a service reload nor nft publication can form a cross-process transaction.

## Decision

One daemon gate serializes explicit Apply, list refresh and pool refresh. A generation
contains an immutable policy snapshot and copied list contents restricted to its sources.
Background updates use committed policy, never pending edits. Pool refresh changes only
membership; list refresh changes only list contents. Scheduler sources retain the union
of committed and pending sources so an unapplied removal cannot stop committed updates.

Peer allocation/insertion and manual database snapshot acquisition share the gate lock.
Reserve all committed peer addresses until explicit Apply releases the old generation;
never restore deleted WG peer rows or authentication state from the routing record.
Reject peer creation while editable allocation fields differ from applied fields. Before
mutation, candidate WG settings must include every stored peer, including disabled peers,
and must not use a peer address as the server address. Pool/address migration is not added.

Generate and validate the complete candidate before network mutation, preserving ADR 0019
pool pruning. Before publishing, install a separate persistent inet nft table dropping
client ingress from wg0 at early prerouting. It covers forwarded traffic, DNS and tproxy;
it does not affect public SSH/panel traffic. Then reconcile candidate WG address,
port and MTU from live key storage without restoring peer rows, ensure policy routing,
install the config and unconditionally restart sing-box, publish the main nft table,
durably save the committed policy/list generation
with owner-only permissions, durably discard obsolete bootstrap authority, and remove
the ingress guard last. Packaged ExecReload may acknowledge an old runtime; it is never
used for publication. Restart terminates that process but does not itself acknowledge
application readiness. A failure after guard
installation leaves clients blocked, reports failure, and requires successful recovery.
Retry must reload even when candidate bytes already match disk. Cancellation observed
before the full commit keeps the guard installed. A healthy background no-op does not
interrupt traffic. Explicit unchanged Apply still guards and repairs WG/route/nft state,
while avoiding a restart of a known runtime.

Install the guard before bringing up wg0 on daemon startup. Recover from the persisted
generation rather than the editable database. Initialize WG from trusted committed/seed
settings; only a genuine legacy installation with neither authority may initialize DB WG
parameters under the guard for out-of-band private-panel access. It cannot release client
traffic or allocate peers until explicit Apply. Untrusted authority never permits fallback.
A fresh installer supplies a separately identified checked seed for its frozen initial
policy. A missing DB with orphan authority is refused. The seed loses authority durably
after committed publication and before traffic resumes; cleanup failure keeps the guard.
A legacy installation without a committed record starts guarded until the operator
explicitly applies its current settings. This
bootstrap requirement avoids pretending that database edits represent the running policy.
Corrupt state stops startup under the guard. Legacy peer creation is refused until
explicit Apply. Existing-install setup preserves runtime config and never automatically
replaces a deleted first peer. DB restore guards ingress and durably invalidates bootstrap
authority before committed authority, then requires explicit Apply. The guard survives daemon failure/restart; network reset
explicitly removes both tables.

This extends the publication contract without replacing ADR 0013: normal direct routing
continues once a coherent generation is active. The temporary interruption affects all
WireGuard clients, including otherwise direct traffic. It is an availability tradeoff
for preventing leakage during transitions and uncertain failures, not a permanent global
kill switch. Existing TCP sessions may be interrupted by runtime reloads.

## Limits

Remote .srs/.json lists are still refreshed autonomously by sing-box; this decision
coordinates daemon-fetched subnet snapshots with the generated configuration, and does
not claim atomicity with sing-box's autonomous remote downloads
([#220](https://github.com/ArghTeam/razdacha/issues/220)). Existing DoH limitations,
privileged external firewall changes, and host compromise are outside this contract.
A packet admitted before the guard transaction completes may already be in flight.

## Consequences

The committed record contains tunnel and peer secrets and must be protected and backed
up like the database. Recovery validates its configuration again. Operators must use
explicit Apply once when upgrading a legacy installation. Publication failures prioritize
visible interruption over silent direct egress. Tests must inspect guard ordering, every
failure boundary, retries, restart, pending edits and forbidden fixture traffic.
