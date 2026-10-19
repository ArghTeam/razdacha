# Staging security validation

Status: planned. This runbook and the linked specifications do not claim that fixes,
deployments or server acceptance checks have been completed.

## Environment boundary and privacy

Only the new environment explicitly designated out of band as staging is authorized
for these checks. Production, including the source host, is excluded from every test,
installation, restart, monitoring, discovery and cleanup action. Do not discover hosts
or infer a target from a reachable address. Stop if the designated target is ambiguous.

Supply connection values through private environment configuration or an approved private
channel. Never commit or publish actual addresses, hostnames, SSH users, device/tunnel
names, keys, tokens, configurations or raw server output. Do not enable shell tracing
or echo secret environment values. Public evidence contains synthetic identifiers and
redacted results only. Keep private snapshots outside repositories with root-only access;
use encrypted transfer/storage if moving them outside the host.

## Ordered delivery

Complete local regression checks, review, controlled staging validation and cleanup for
one change before advancing to the next. These are sequential delivery dependencies.

| Order | Specification | Primary staging observation | Current status |
|---|---|---|---|
| 1 | [#214: coherent network state](../tasks/214-coherent-network-state.md) | List refresh and Apply publish coherent policy; failures cannot expose direct traffic | Planned, not executed |
| 2 | [#215: safe peer deletion](../tasks/215-safe-peer-deletion.md) | Removing the selected client never broadens its rule | Planned, not executed |
| 3 | [#216: clear UI secrets](../tasks/216-clear-ui-config-secrets.md) | Synthetic key/configuration disappears on modal close, replacement and logout | Planned, not executed |
| 4 | [#217: safe restore](../tasks/217-safe-cross-host-restore.md) | Synthetic restore preserves identity and starts outgoing WireGuard paused | Planned, not executed |
| 5 | [#218: isolated health](../tasks/218-isolated-tunnel-health.md) | DNS, transport and HTTP/TLS signals stay distinct; client rejects remain effective | Planned, not executed |

## Preconditions before each staging run

1. Reproduce the defect with deterministic local unit/browser/integration fixtures,
   then verify the fix and its negative/failure cases. Use appropriate Linux namespaces
   or disposable instances for networking and restore checks. Run required build/static
   checks and review maintained context before deployment.
2. Confirm the explicitly designated staging target out of band. Record privately its
   current version, binary/configuration identities, service state, effective flags,
   tunnel activation states, panel access mode and available CPU/RAM/disk headroom.
   Do not inspect or monitor production as part of validation.
3. Capture a consistent fresh snapshot of CURRENT staging database and relevant runtime
   configuration, binaries, service units/drop-ins, effective flags, certificates and
   network state. Use a supported consistent database backup mechanism rather than
   copying a live SQLite file without its journal/WAL. Restrict snapshots to root.
4. Establish a reliable recovery path before changing anything: retain administrative
   access, validate snapshot integrity/completeness and rehearse recovery in an isolated
   disposable environment. Account for version/schema compatibility and network access.
   If recovery cannot be demonstrated, stop the staging run.
5. Never use the original cross-host migration archive as rollback. It may reactivate
   credentials still used elsewhere. Preserve current staging enablement/paused states;
   never activate cloned real outgoing WireGuard credentials, even briefly.
6. Prepare disposable synthetic clients, rules and isolated WireGuard server/client
   fixtures, plus controlled local HTTP list, DNS and HTTP/TLS sources as needed.
   Contain their endpoints in disposable namespaces/instances; do not point fixtures
   at real provider endpoints. Do not use provider credentials as test fixtures.

## Controlled execution and evidence

Use low-volume fixture traffic and bounded timeouts. No external IP-echo services,
third-party test sites, production traffic, load tests or penetration tests are part
of this plan. Do not change actual client routing to make a diagnostic check pass.

For #214, observe selected and forbidden traffic paths while refreshing a fixture list,
saving an unapplied edit, injecting validation/publication failures and overlapping Apply.
For #215, use two disposable clients and an isolated private outbound or reject rule;
compare the unassigned client's behavior before and after deleting the assigned client.
For #216, use a generated fixture key, test download, all close paths, modal replacement
and normal/expired-session logout without a page reload. Complete delayed configuration
responses after close, replacement, logout and session expiry; none may repopulate keys.
Avoid capturing real configs.
For #217, restore synthetic archives only into disposable destinations and exercise first
boot, host adaptation, failed validation and rollback without contacting a source host.
For #218, run the DNS/transport/HTTP/TLS failure matrix while proving that fixture client
reject rules still reject. Label unavailable observations as unknown rather than healthy.

Record the tested commit/version, fixture case, expected/observed result, and rollback
outcome with all connection and secret values removed. Mark each case passed, failed,
blocked or not executed; do not replace evidence with an unsupported green status.
Keep raw traces, browser captures and configuration snapshots private and access restricted.

## Independent security review gates

The author cannot approve their own implementation. Use two gates:

1. Before deploying risky code even to an isolated staging canary, an independent reviewer
   checks the threat/failure model, reproducible local baseline-fails/fix-passes evidence,
   adversarial matrix, approved scope and recovery plan. Only controlled fixture testing
   may proceed after that gate.
2. After staging evidence is available, review the final diff, tests, maintained context,
   negative results and cleanup/recovery outcome before declaring merge/release readiness.

For #214 and #217, require two independent reviewer passes: a threat/failure-model review
and a final diff/tests/docs/evidence review, performed by independent reviewers. For #215,
#216 and #218, require a reviewer separate from the author plus primary owner review.
Re-review subsequent material changes. No unresolved P0/P1 finding may be marked ready to
merge; unknown or untested behavior is recorded explicitly. Approval grants no automatic
merge, release or production deployment authority.

Review failure, concurrency, rollback, restart/first boot, unapplied edits and stale
asynchronous responses where relevant. A green panel alone is insufficient: routing/leak
cases need wire-level observations at a controlled fixture sink proving forbidden direct
traffic is absent and pre-existing protections still hold. Keep captures private; publish
only redacted assertions. Avoid claiming that an unobserved path was tested.

The review evidence records redacted test commands, revision, expected/observed results,
review findings and disposition, approved scope, and a rollback summary. Never include
private host output or connection/credential values. These are future implementation
gates; this planning document does not claim that any fix passed them.

## Cleanup and rollback

Remove disposable peers, rules, processes, namespaces and fixture artifacts. Restore
current pre-test user configuration and activation flags without undoing the reviewed fix
when validation succeeds. Verify configuration identity and service health locally on
staging. On failure, restore compatible binaries/database/configuration from the fresh
pre-test snapshot and recheck its effective flags, panel access and activation states.
Never change production to recover staging. If recovery fails, stop subsequent tasks and
report the blocker with redacted evidence; keep the recovery snapshot available privately.

## Documentation gate

Each implementation PR updates relevant maintained context and behavior documentation.
Write a new ADR before changing an accepted decision; historical ADRs are not rewritten.
This planning change does not alter runtime invariants or introduce an architecture decision.
