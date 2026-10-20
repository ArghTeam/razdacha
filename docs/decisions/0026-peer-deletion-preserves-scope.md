# 0026. Peer deletion preserves selected scope

Date: 2026-10-20 · Status: proposed; clarifies ADR 0013

## Context

Deleting the last selected client previously changed its rule to all clients. This
implicitly granted private tunnel access or broadened rejection. An empty source
matcher in sing-box also means all sources, so retaining a selected-but-empty enabled
rule as a reject is unsafe.

## Decision

Peer deletion atomically removes deleted and dangling selected IDs. A selected rule
without an existing client stays selected, with an empty list and disabled activation.
Its identity, priority, destinations and tunnel references are preserved. Partial
selections keep their activation. Rule writes validate existing selected IDs inside
the same transaction as the write, preventing a stale PATCH from resurrecting a
deleted selection. Duplicate IDs are normalized. Empty selections are writable only
while disabled; reassignment alone never enables a disabled rule.

The generator skips selected rules with no existing source before constructing sets,
chains or rejection actions, including malformed legacy empty/dangling selections.
Existing disabled clients remain source-constrained rejection under ADR 0013. This
clarifies its missing-client case: no existing client cannot become a global reject.

The deletion response preserves `ok: true` and reports affected and inactivated rule
IDs from the transaction. Rule responses expose `inactive_reason: no_selected_peers`
for an orphan. The panel names affected rules before deletion, refreshes rules after
success, and explains explicit reassignment and activation.

No schema migration guesses whether an existing all-client rule resulted from the
old bug or intentional configuration. Existing all-client rules remain unchanged;
operators must audit their intended scope. Raw legacy selected orphans are safely
ignored by generation and normalized when touched by deletion or a valid rule write.

## Consequences

Deleting a client cannot grant another client its routing or block policy. Orphaned
rules remain editable and recognizable. A policy change still requires explicit Apply;
committed routing authority and reserved addresses retain ADR 0023 semantics. Recovery
never restores deleted live WireGuard identities. Tests and isolated staging must cover
both private-outbound grant and reject broadening, multiple clients and recovery.
