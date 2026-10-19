---
type: spec
tracker: "#218"
---
# Isolated tunnel health signals

## Goal and problem
A client DNS rule can reject a diagnostic hostname before the tunnel transport is tested,
producing a false down status. Report observable DNS, WireGuard transport and HTTP/TLS
signals separately without weakening client routing or fail-closed policy.

## Domains touched
- [API](../context/api/index.md): diagnostics, check responses and signal definitions.
- [Singbox](../context/singbox/index.md): isolated diagnostic resolution/probing context.
- [UI](../context/ui/index.md): signal labels and unavailable/unknown states where needed.

## Decisions
Implement after [#217](217-safe-cross-host-restore.md). Define the isolated probe context
before code; use a new ADR if required by an accepted architectural decision. Preserve
[ADR 0013](../docs/decisions/0013-unavailable-tunnel-rejects.md). Keep the existing eight-second
bound; coordinate with [#166](https://github.com/ArghTeam/razdacha/issues/166) without duplicating
its slow-probe work. Unobservable transport health must remain unknown, not inferred
from ICMP or an HTTP response alone.

## Acceptance criteria
- [ ] A regression has rejected client probe DNS plus working synthetic transport;
      client rejection alone cannot mark that transport down.
- [ ] Actual DNS and transport failures are distinguished from downstream HTTP/TLS failure.
- [ ] API/UI label available signals and explain unknown states and probe limitations.
- [ ] Diagnostic success never depends on changing client rules or adding a client bypass.
- [ ] A client reject rule still rejects during and after diagnostic checks.
- [ ] API, diagnostic, singbox and any changed UI context document final signal semantics.

## Validation
Planned; not executed by this planning change. Use local fake DNS, a synthetic WireGuard
server/client pair and controlled HTTP/TLS endpoints for the failure matrix. Follow with
an isolated integration check, then the [staging runbook](../docs/11-staging-validation.md).
Track redacted evidence in [issue #218](https://github.com/ArghTeam/razdacha/issues/218).

## Security review gates
The implementation author cannot approve their own change. Before an isolated staging
canary, an independent reviewer must assess the local regression, threat/failure model
and proposed scope/rollback. Require a separate reviewer and primary owner review of the
final diff, tests, documentation and staging evidence.
Final acceptance requires baseline-fails/fix-passes evidence, adversarial negative
cases and controlled staging observations under the [runbook](../docs/11-staging-validation.md).
No unresolved P0/P1 findings may be marked ready to merge. Unknown results stay explicit.
Review approval does not authorize merge or release. These reviews are planned, not completed.
