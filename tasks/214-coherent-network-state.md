---
type: spec
tracker: "#214"
---
# Coherent applied network state

## Goal and problem
A scheduled plain-list refresh must update sing-box matching and nft subnet sets from
one applied policy. Today, inline sing-box lists can remain stale while nft is updated;
nft also reads pending database edits that the operator has not applied.

## Domains touched
- [Lists](../context/lists/index.md): list snapshots and refresh notification.
- [Netstack](../context/netstack/index.md): subnet publication and daemon wiring.
- [Singbox](../context/singbox/index.md): full configuration generation and apply gate.

## Decisions
First in the delivery sequence. Preserve [ADR 0013](../docs/decisions/0013-unavailable-tunnel-rejects.md)
and existing pending-edit semantics. Document the chosen applied snapshot, serialized
publication boundary, and failure behavior before implementation. Add an ADR if an
accepted decision must change; this specification does not select a transaction design.

## Acceptance criteria
- [ ] A deterministic regression reproduces stale inline list matching before the fix.
- [ ] A refresh changes domain and subnet matching coherently in both routing layers.
- [ ] Saved but unapplied rule/subnet edits remain unapplied during scheduled refresh.
- [ ] Validation, sing-box apply, or nft failure retains the prior effective policy or
      fails closed without a direct-route interval.
- [ ] Concurrent Apply and refresh cannot publish mixed generations.
- [ ] Lists, netstack and singbox context, plus apply documentation, reflect the result.

## Validation
Planned; not executed by this planning change. Start with focused snapshot/failure tests,
then a controlled local HTTP plain-list source and disposable Linux network namespace.
Check both positive routing and forbidden direct traffic during failed/concurrent apply.
Follow the [staging runbook](../docs/11-staging-validation.md) only after local checks pass.
Track delivery and evidence in [issue #214](https://github.com/ArghTeam/razdacha/issues/214).

## Security review gates
The implementation author cannot approve their own change. Before an isolated staging
canary, an independent reviewer must assess the local regression, threat/failure model
and proposed scope/rollback. Require two independent reviewer passes: one focused on the
threat/failure model and one on the final diff, tests, documentation and staging evidence.
Final acceptance requires baseline-fails/fix-passes evidence, adversarial negative
cases and controlled staging observations under the [runbook](../docs/11-staging-validation.md).
No unresolved P0/P1 findings may be marked ready to merge. Unknown results stay explicit.
Review approval does not authorize merge or release. These reviews are planned, not completed.
