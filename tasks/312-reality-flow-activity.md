# Authenticated REALITY flow activity

Tracker: #312. Decision: [ADR 0038](../docs/decisions/0038-authenticated-reality-flow-observations.md).

## Scope and checkout

Authorized repository: razdacha and its worktrees only. Primary checkout:
`/Users/romanzhigalkin/Web/razdacha`; remote/base `origin/main`, freshly fetched
`93bf487cd9242993bfa5f216edaba6b30fdbfa66`. Feature worktree:
`.codex/worktrees/feat-312-reality-activity`, branch `feat/312-reality-activity`.
No production, cloud resource mutation or legacy tunnel activation.

## Acceptance

1. Attribute open flows only to the exact authenticated admitted user, including
   independent clients behind one public address.
2. Preserve direct/tunnel TCP/UDP policy, guard behavior and WG.
3. Bound and coalesce observations; stale/error/malformed/unsupported remains
   unavailable. Discard reads spanning publication or revocation.
4. Show open-flow counts separately from WG counters without claiming idle online
   presence or REALITY byte accounting.
5. Explain gateway DNS and full-profile import limitations.
6. Verify parser isolation, lifecycle races, browser states and rule expansion.

## Validation

Implementation checks pending. Synthetic browser and loopback tests do not
establish Android DNS capture; device confirmation remains separate.
