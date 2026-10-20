---
type: spec
tracker: "#215"
---
# Safe peer deletion

## Goal and problem
Deleting the last selected peer previously promoted its rule to all peers. Deletion must
never grant another client access to a private tunnel or implicitly broaden a reject rule.

## Domains touched
- [Store](../../context/store/index.md): peer deletion and scope representation.
- [Singbox](../../context/singbox/index.md): effective matching of orphaned rules.
- [UI](../../context/ui/index.md): deletion explanation and explicit reassignment.
- [API](../../context/api/index.md): communicate affected rules where required.

## Decisions
Implemented after [#214](214-coherent-network-state.md) and native coherence
[#220](220-native-ruleset-coherence.md). Disabled selected orphans preserve rule identity,
conditions and references for explicit reassignment; historical all-client rules are
not guessed or migrated. ADR 0026 clarifies ADR 0013 for missing selected clients.

Chosen semantics: disabled selected orphan, specified before implementation in
[ADR 0026](../../docs/decisions/0026-peer-deletion-preserves-scope.md).

## Acceptance criteria
- [x] Removing the only selected peer never changes scope to all or affects another peer.
- [x] Removing one of several selected peers preserves the remaining selection.
- [x] API/UI identify affected rules and explain their inactive/reassignment state.
- [x] Store and sing-box regressions cover last-peer and multiple-peer cases.
- [x] Two synthetic clients prove that deleting the assigned client does not give the
      other client access through a private outbound or newly apply its reject rule.
- [x] Store, singbox and UI context record the final behavior; update API context if changed.

## Validation
Local store/API/generator tests, full Go suite, full race suite, vet, pinned sing-box
DNS/runtime tests and Linux fixture compilation pass. Independent source and private
runner review preceded the controlled four-case wire run. Final documentation/evidence
review and current CI remain merge gates. No installed daemon was changed.

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
Revision-specific results and observation limits are recorded below and in the
[staging runbook](../../docs/11-staging-validation.md).

## Security review gates
The implementation author cannot approve their own change. Before an isolated staging
canary, an independent reviewer must assess the local regression, threat/failure model
and proposed scope/rollback. Require a separate reviewer and primary owner review of the
final diff, tests, documentation and staging evidence.
Final acceptance requires baseline-fails/fix-passes evidence, adversarial negative
cases and controlled staging observations under the [runbook](../../docs/11-staging-validation.md).
No unresolved P0/P1 findings may be marked ready to merge. Unknown results stay explicit.
Independent source and runner reviews completed before execution. Final documentation,
actual evidence and current CI remain subject to primary owner review. Review approval
does not authorize merge, release or deployment.

## Accepted controlled evidence

Tested source: `14e28b3a80788b7b15fdbf730d17b6fa1a72bab9` in
[PR #225](https://github.com/ArghTeam/razdacha/pull/225). The baseline is pristine
`8b149e07085b2594dc9f790e9a96eeff7ef382f7` with exactly three reviewed fixture
overlays. Independent provenance checked all 431 original files, exact overlays,
241 fixed and 237 baseline Go/embed inventory entries, and both compiled binaries.
The fixture runtime was the pinned sing-box v1.12.25. The fixed Go/embed digest is
`05bb18cd92e67ce849d6f61a19567e8830fa74835144875431a18d4d25e6c7c7`;
the baseline digest is `35f1f024c00df3a6d9de6da93bf8d6673df90d0109fcbab6324aeb8e758506c2`.
Fixed binary SHA-256: `a286ffab33287d0590d106e09944a29d8b96bae3cdf812500e1adc7ecdf48edd`.
Baseline binary SHA-256: `88ed3361fc627f8f132fdef0aabd27ac6658e8739729481e97f97e1e2da913e1`.

The single controlled wrapper execution completed all four ordered cases:

| Case | Result | Zero-drop capture ACKs | Case-log SHA-256 |
|---|---|---|---|
| fixed-grant | Nested/outer Go PASS; private/direct identity controls passed | 49 | `856998153b186e3e616fbfc133e4ca40b13ce736e0becb4e8fd7e2b818ac6eda` |
| fixed-reject | Nested/outer Go PASS; selected refusal and unrelated controls passed | 45 | `95efc435f03b7fd456216dae39286b6f74ff6bc889796a4ce9cbf98c44d4acb6` |
| baseline-grant | Expected nested/outer Go FAIL after unrelated B's private delivery proof | 12 | `df589dc65afa72e67d9257548d851a72b6026b65bd8823a6d765976d09556654` |
| baseline-reject | Expected nested/outer Go FAIL after unrelated B's policy refusal proof | 8 | `e6627eced63db54451c25575ee6a027d2722934dba0f56d398c254533e299859` |

Each fixed case emitted all seven ordered stage markers and its final success marker.
The baseline fatal markers followed their complete positive-control and broadening
assertions. Probe details are fail-fast assertions in the frozen source/binaries,
not separately printed raw traffic transcripts. The 114 collector ACK/drop markers
and nested/outer summaries were independently checked. No generic failure, panic,
observer error or missing proof was accepted as a desired baseline failure.
The supervisor exited zero without wrapper or supervisor errors.

All seven downloaded result files matched the remote size/SHA inventory. The private
evidence manifest SHA-256 is
`109d17c5cba48fe29c6d21fd8ed9e1b6fc019db04d83bcdd3b2d0f3788d0de7b`.
Snapshot-to-BEFORE and snapshot-to-AFTER differed only in seven `checked_at` and three
`ok_at` health-timestamp cells. Immediate BEFORE-to-AFTER full database semantics and
policy semantics were exactly equal, with zero timestamp changes. Schema and all
other cells remained stable; this does not imply whole SQLite backup byte equality.
Host/file inventories, installed source files, services and policy identity were
stable. All four pinned uploads were removed, and no owned process/group or private
case-directory process remained. Private results and current snapshots were retained.

Synthetic Playwright verification passed against the frozen UI file hashes, including
the deletion/orphan/reassignment flow. Independent source, artifact/runner and actual
wire evidence reviews passed. Final documentation review and current CI remain merge
gates; the author does not approve or merge this change.

## Observation limits

The fixture exercises actual nft/route/sing-box behavior with two bound veth source
identities. It does not test an encrypted WireGuard handshake or real provider keys.
A's address is reserved through committed recovery before Apply; D's reuse is checked
after Apply and applied recovery. A further refresh/recovery after D allocation was
not separately exercised. Finite zero-drop barriers do not prove universally lossless
capture, every possible interleaving or future traffic protection. No installed daemon
deployment, host service restart, provider-credential activation, production change
or release occurred.
