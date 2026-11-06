# 330 — Explicit onboarding recovery review

Candidate-only audit F12 fix under #324. Remote origin; target epic/297-rac-reality-integration.
Fresh base: 09e8777ed5abeb09ab039e708ebf54ec27b2121e.
Designated primary: /Users/romanzhigalkin/Web/razdacha/.codex/worktrees/epic-297-rac-reality-integration.
Feature: /Users/romanzhigalkin/Web/razdacha/.codex/worktrees/fix-330-onboarding-rereview.

Explicit review reads accepted current policy under the publication gate, returning
safe summary values and opaque old/current revisions. Explicit confirmation refreshes
only onboarding metadata; it never changes policy, clients, credentials or pool inventory.
The reviewed fingerprint and metadata revision must still match. Normal guarded Apply
remains required. Pending/uninitialized/guarded policy cannot be reviewed as accepted.
Closing does not confirm. Old dialog/session reads cannot replace a newer draft;
confirmed writes preserve same-session pending effects after closure.

Verify failed setup Apply, accepted ordinary MTU change, raw retry conflict, explicit
review/confirmation, successful completion preserving accepted policy and identities.
Reject stale revisions, concurrent changes, guarded state and unauthenticated access.
