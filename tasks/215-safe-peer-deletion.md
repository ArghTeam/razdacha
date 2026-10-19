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

## Acceptance criteria
- [ ] Removing the only selected peer never changes scope to all or affects another peer.
- [ ] Removing one of several selected peers preserves the remaining selection.
- [ ] API/UI identify affected rules and explain their inactive/reassignment state.
- [ ] Store and sing-box regressions cover last-peer and multiple-peer cases.
- [ ] Two synthetic clients prove that deleting the assigned client does not give the
      other client access through a private outbound or newly apply its reject rule.
- [ ] Store, singbox and UI context record the final behavior; update API context if changed.

## Validation
Planned; not executed by this planning change. Run focused store, API and generator tests;
then use synthetic clients and an isolated outbound/reject fixture under the
[staging runbook](../docs/11-staging-validation.md). Record redacted evidence in
[issue #215](https://github.com/ArghTeam/razdacha/issues/215).

## Security review gates
The implementation author cannot approve their own change. Before an isolated staging
canary, an independent reviewer must assess the local regression, threat/failure model
and proposed scope/rollback. Require a separate reviewer and primary owner review of the
final diff, tests, documentation and staging evidence.
Final acceptance requires baseline-fails/fix-passes evidence, adversarial negative
cases and controlled staging observations under the [runbook](../docs/11-staging-validation.md).
No unresolved P0/P1 findings may be marked ready to merge. Unknown results stay explicit.
Review approval does not authorize merge or release. These reviews are planned, not completed.
