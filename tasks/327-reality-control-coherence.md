# REALITY control and inventory coherence

Issue: #327; umbrella: #324; audit F05–F08.

Validate merged peer/settings objects under the publication gate before installing
revocation guards. Preserve fail-closed handling after potentially durable writes.
Merge settings PATCH against fresh gated state, preserving omitted fields and
secrets. Inventory uses one coherent metadata snapshot and preindexed revisions;
secret serialization belongs only to explicit delivery. Keep pending delivery,
committed active endpoints and observation authority checks.

Primary checkout: `/Users/romanzhigalkin/Web/razdacha`; remote: `origin`; base: `main`.
Fresh fetched base: `8dc7cdbe50d6b6639d06cabfc7890fa75d7172cf`.
Feature checkout: `.codex/worktrees/fix-327-reality-control-coherence`.
Branch: `fix/327-reality-control-coherence`. Deliver an open PR without merging.

Acceptance: invalid disable has no guard/auth side effects; accepted failed
revocation remains guarded; concurrent partial saves preserve unrelated fields;
inventory scales linearly without URI/JSON exports; eligibility matches delivery
for pending/active/stale/disabled/guarded clients.

## Verification and maintained contracts

- Full `CGO_ENABLED=0 go test ./...` passes.
- Focused race tests cover invalid revocation, guarded write failure, settings
  interleavings, delivery/revision coherence and inventory scaling.
- Instrumented SQL reads remain exactly four at 1 and 100 clients; one inventory
  publisher call replaces per-peer status/export checks. No latency claim is made.
- Actual authenticated HTTP regressions return 400 for invalid peer/global disable
  and preserve sibling credentials, public ingress and WireGuard ingress.
- API invariants/log document final behavior. This restores ADR 0032/0036 contracts;
  no new architectural decision or browser response-shape change is introduced.
- Legacy inventory publishers keep per-peer compatibility. A legacy REALITY
  publisher lacking gated partial merge refuses settings PATCH safely; the daemon
  implements the gated merge contract.
