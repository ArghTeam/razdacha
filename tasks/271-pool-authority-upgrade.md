# 271: Pool catalog refresh in upgrade authority proofs

## Scope and provenance

Authorized repository: `ArghTeam/razdacha` only. Primary checkout:
`/Users/romanzhigalkin/Web/razdacha`; remote `origin`; target `main`; verified base:
`5bc7631b2a6b1a82d6fd993837d9f8b9d21a582a`. Feature checkout:
`.codex/worktrees/fix/271-pool-authority-upgrade`.

This changes only the bounded in-place operator/controller and its proof evidence,
tests and maintained packaging documentation. No daemon behavior, runtime config
normalization, new release or host operations are included. Historical failed
operation evidence is preserved; the new proof format is not a license to replay
old unfinished ledgers.

## Problem and final behavior

Startup can republish automatic SourcePool catalog/time fields in committed
authority. Database acceptance already permits those two fields on paired pool
rows, but comparing the entire generation digest rejects both forward startup
and rollback startup despite otherwise preserved policy.

The helper still reads owner-only version-four authority and verifies every current
native artifact. It now retains the exact original raw JSON, its SHA256 and the
existing typed canonical digest in private evidence. It checks that raw evidence
and the artifact-verified typed generation describe the same observed generation;
a concurrent change fails the proof.

The controller validates duplicate-free authority JSON during preflight before any
service stop. It pairs tunnels by unique ID, requires unchanged source, and removes
only `pool` and `pool_updated_at` from copies of matching SourcePool rows for
comparison. It retains strict generation kind/version, list/native content and
references, tunnel order, keys, activation, settings, peers, rules and unknown
fields/types. Original evidence and historical snapshots are never mutated.

## Verification

- Generation-shaped candidate startup and rollback fixtures republish pool catalog
  fields; acceptance succeeds and original raw snapshot/ledger evidence remains.
- Adversarial changes to keys, sources, activation, peer egress, rules, lists/native
  authority, generation identity/version, duplicate tunnel IDs, order and unknown
  field types are rejected. Duplicate baseline JSON fails check/run before stop.
- The original whole-authority digest comparison fails both startup regressions;
  the corrected paired-field comparison passes.
- Linux helper tests retain exact raw formatting/unknown fields and independently
  reject a corrupted native artifact after an earlier successful proof.
- Focused Python matrix, Linux helper cross-build/static checks and independent
  review are required before delivery. No live connection/deployment result is
  asserted by this implementation record.
