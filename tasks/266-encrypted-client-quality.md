# Issue 266: Encrypted client connection-quality fixture

## Goal

Extend the opt-in isolated network fixture with an actual kernel WireGuard ingress,
while preserving the existing fixture's default behavior. Exercise shared/selective
and full-egress routing, unavailable exits, exact payload integrity across the MTU,
persistent TCP/UDP connections, and bounded deterministic loss and recovery.

## Safety and acceptance criteria

- Require both explicit network-namespace and quality opt-ins.
- Refuse host or populated starting namespaces before any network mutation.
- Generate disposable reciprocal keys; never read installed profiles or services.
- Use only private fresh NET+MOUNT namespaces and documentation-only underlays.
- Verify a recent encrypted handshake and increasing receive/transmit counters.
- Reuse independent sink capture barriers and packet-drop accounting.
- Record fragmentation/DF behavior and verify exact payloads, not only byte counts.
- Do not claim real-provider performance or successful Google Meet calls.
- Independent review precedes any server-side namespace canary.

## Checkout provenance

- Primary checkout: `/Users/romanzhigalkin/Web/razdacha`
- Remote and base: `origin/main`
- Verified base: `1a3b84e81f5b50b6596f7e259fae137d4ae31def`
- Feature checkout: `.codex/worktrees/test/266-encrypted-client-quality`
- Branch: `test/266-encrypted-client-quality`

## Validation

Portable helper tests, daemon tests, Linux compile/vet, static test-binary hashing,
and an independently reviewed Linux namespace execution. Runtime evidence is
reported separately from build-only checks.

## Reviewed runtime evidence

- Tested source: `fc66990881c7a14985c05dcfb91e20c797271db0`.
- Static Linux amd64 ELF SHA-256:
  `66b060a8ebb07f8355bce0b477c5364bad8e251daa319fc1f9858af24d89463c`.
- Isolated execution: exit 0, 22.07 seconds, no skips, 33 MiB peak memory.
- Selective/full-egress/shared stages: encrypted handshake and positive RX/TX
  deltas, exact TCP/UDP MTU matrices, persistent client sockets and reconnect.
- Capture: 47 zero-drop barriers; deterministic impairment: one netem drop,
  bounded receive failure and successful new TCP/UDP traffic after removal.
- Production service PIDs/restarts, binary/configuration hashes, schema and stable
  policy/peer/settings digests, WireGuard identity and absent guard unchanged.
- Cleanup: no owned test processes, collected inactive unit, private 0600 evidence.
- Early isolated runs exposed fixture-only return-DF and raw-capture fragmentation
  assumptions; both were corrected and reviewed before the successful rerun.
- Limits remain explicit: synthetic exits, no Meet/provider/production key-lifecycle
  claim, no established-socket survival-through-loss claim, and no proof excluding
  arbitrary incomplete fragments held/dropped before socket delivery.
