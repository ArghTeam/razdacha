---
type: spec
tracker: "#215"
---
# Safe peer deletion

## Goal and problem
Deleting the last selected peer currently promotes its rule to all peers. Deletion must
never grant another client access to a private tunnel or implicitly broaden a reject rule.

## Domains touched
- [Store](../context/store/index.md): peer deletion and scope representation.
- [Singbox](../context/singbox/index.md): effective matching of orphaned rules.
- [UI](../context/ui/index.md): deletion explanation and explicit reassignment.
- [API](../context/api/index.md): communicate affected rules where required.

## Decisions
Implement after [#214](done/214-coherent-network-state.md). Before code, choose and document
safe empty-scope semantics: disabled orphaned rule or selected-but-empty rule. Preserve
rule identity, conditions and references for explicit reassignment. Document compatibility
and migration behavior; add an ADR if the choice supersedes an accepted decision.

Chosen semantics: disabled selected orphan, specified before implementation in
[ADR 0026](../docs/decisions/0026-peer-deletion-preserves-scope.md).

## Acceptance criteria
- [x] Removing the only selected peer never changes scope to all or affects another peer.
- [x] Removing one of several selected peers preserves the remaining selection.
- [x] API/UI identify affected rules and explain their inactive/reassignment state.
- [x] Store and sing-box regressions cover last-peer and multiple-peer cases.
- [ ] Two synthetic clients prove that deleting the assigned client does not give the
      other client access through a private outbound or newly apply its reject rule.
- [x] Store, singbox and UI context record the final behavior; update API context if changed.

## Validation
Local store/API/generator tests, full Go suite, full race suite, vet, pinned sing-box
DNS/runtime tests and Linux fixture compilation pass. Source review, current CI and
isolated wire acceptance remain required before merge. No installed daemon is changed.

The wire adapter uses distinct bound source identities on a disposable veth; it does
not prove an encrypted WireGuard handshake. A custom contained SOCKS5 relay verifies
nonce-matched TCP and UDP delivery plus private reply identity, distinct from direct
sink echo. DNS probes verify source binding and exact real/FakeIP/REFUSED outcomes.

Four cases run in order: fixed-grant, fixed-reject, baseline-grant, baseline-reject.
Selector: `^TestNetworkGenerationWire$`; opt-in `RAZDACHA_NETNS_TEST=1`, absolute
`RAZDACHA_TEST_SINGBOX`, and `RAZDACHA_WIRE_PEER_CASE=grant|reject`. Baseline uses
`security215_baseline` over pristine base source plus the shared namespace/helper
fixtures and baseline adapter only. Limits remain Go 80 seconds, inner context 60
seconds, supervisor 90 seconds per case; all sinks and resolution stay contained.

Each case first proves A selected and B direct, with independent direct controls.
Fixed cases then check B after deletion, list refresh, pool refresh, committed recovery,
explicit Apply, applied recovery and disabled reassignment, followed by explicit B
activation as a positive control. Before Apply, historical A policy/address authority
is retained; after Apply a new D receives A's former address without inheriting its
orphaned policy. Live store rows never revive A. Local daemon tests also cover fresh
recovery/address reservation; the veth adapter does not configure real authentication.

The exact precondition marker is `215-PEER-PRECONDITIONS-PASS case=X`. Fixed cases emit
seven ordered `215-FIXED-PEER-STAGE-PASS case=X stage=Y` markers (stages above) and a
final `215-FIXED-PEER-WIRE-PASS case=X`. Baseline accepts only the explicit fatal marker
`215-BASELINE-PEER-BROADENING-OBSERVED case=X: unrelated client inherited selected policy`
after proving the broadened database/generated source scope, B's private delivery or
valid DNS refusal plus protected TCP/UDP denial, and simultaneous B direct controls.
Generic failure, capture error, timeout or missing proof never counts as regression red.

Both nested and outer Go summaries are required. Existing raw capture ACK/drop checking
emits `220-CAPTURE-BARRIER-PASS drops=0`: counts are respectively 49, 45, 12 and 8.
Finite zero-drop capture is observation, not a universal packet-loss claim.
Actual revision-specific remote evidence remains pending under the
[staging runbook](../docs/11-staging-validation.md).

## Security review gates
The implementation author cannot approve their own change. Before an isolated staging
canary, an independent reviewer must assess the local regression, threat/failure model
and proposed scope/rollback. Require a separate reviewer and primary owner review of the
final diff, tests, documentation and staging evidence.
Final acceptance requires baseline-fails/fix-passes evidence, adversarial negative
cases and controlled staging observations under the [runbook](../docs/11-staging-validation.md).
No unresolved P0/P1 findings may be marked ready to merge. Unknown results stay explicit.
Review approval does not authorize merge or release. These reviews are planned, not completed.
