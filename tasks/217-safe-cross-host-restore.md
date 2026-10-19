---
type: spec
tracker: "#217"
---
# Safe cross-host restore

## Goal and problem
A full backup carries outgoing credentials that may still be active elsewhere. A
cross-host restore must retain incoming identity, peers, rules and references while
preventing cloned outgoing WireGuard credentials from activating on first boot.

## Domains touched
- [Packaging](../context/packaging/index.md): restore, destination adaptation and rollback.
- [Store](../context/store/index.md): preserved identities and explicit activation state.
- [Singbox](../context/singbox/index.md): safe first generated configuration.
- [Netstack](../context/netstack/index.md): destination networking constraints.

## Decisions
Implement after [#216](216-clear-ui-config-secrets.md). Before code, write an ADR defining
cross-host restore and paused outgoing WireGuard first boot; preserve or explicitly
supersede [ADR 0016](../docs/decisions/0016-backup-full-and-encrypted.md).
Never contact, disable or rewrite the source. Activation requires an explicit destination
operator action. Keep unavailable-tunnel rules fail closed under
[ADR 0013](../docs/decisions/0013-unavailable-tunnel-rejects.md).

## Acceptance criteria
- [ ] Fixture restore preserves server/peer identity, rule IDs, conditions, scopes and references.
- [ ] Outgoing WireGuard tunnels remain paused through restore and first startup.
- [ ] Source hosts are never contacted or modified by restore or its validation.
- [ ] Failed validation leaves prior destination state recoverable from its pre-restore backup.
- [ ] Destination checklist covers hardware/interface differences, public endpoint,
      WAN/NAT mapping, panel exposure and certificate SANs before client cutover.
- [ ] Isolated synthetic restore/rollback checks prove activation and recovery behavior.
- [ ] Packaging, store, singbox and netstack context and backup/restore docs are updated.

## Validation
Planned; not executed by this planning change. Start with synthetic databases and disposable
instances/namespaces. Use independently generated fixture server/client credentials;
never enable cloned provider keys. Follow the [staging runbook](../docs/11-staging-validation.md)
for any smoke test, using current destination state for rollback, never the original
migration archive. Track evidence in
[issue #217](https://github.com/ArghTeam/razdacha/issues/217).

## Security review gates
The implementation author cannot approve their own change. Before an isolated staging
canary, an independent reviewer must assess the local regression, threat/failure model
and proposed scope/rollback. Require two distinct reviewers, independent of the author and each other:
one reviews the threat/failure model; the other reviews the final diff, tests,
documentation and staging evidence. Two rereads by the same reviewer do not qualify.
Final acceptance requires baseline-fails/fix-passes evidence, adversarial negative
cases and controlled staging observations under the [runbook](../docs/11-staging-validation.md).
No unresolved P0/P1 findings may be marked ready to merge. Unknown results stay explicit.
Review approval does not authorize merge or release. These reviews are planned, not completed.
