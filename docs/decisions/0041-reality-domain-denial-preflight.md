# 0041. Reject provably denied REALITY hostnames before resolution

Date: 2026-11-06 · Status: **accepted** · Tracker: #329

## Decision

Extend ADRs 0032 and 0037 with reject-only route preflight before shared REALITY
resolution, after authentication, initial private/DNS guards and whole-client
terminal rules. Supersede no decision. Keep ADR 0024 captured-DNS policy, including
its known C1 priority boundary, unchanged.

For a nonempty exact domain-denial projection and admitted authenticated scope,
reject hostname/FakeIP destinations only when no earlier applicable positive
policy can win. Earlier domain-only positives shadow by their exact domain
projection and authenticated scope. Any possible address-dependent positive
branch shadows its entire scope. Unknown/legacy native summaries are conservative;
complete verified ordinary and transport summaries are needed to prove domain-only.
Native context restrictions are not analyzed, so some ultimately denied names
still resolve. The guarantee covers provable early denials, not universal zero DNS.

Use the inverse of IP versions 4 or 6 as the hostname gate: pinned 1.12.25 converts
recognized FakeIP to FQDN before classifying IP version. Preserve numeric sniffing,
full original matcher ordering, post-resolution private rejection and positive
singleton matched-rule identity. Emit grouped auth sets, with an explicit bounded
construction budget whose exhaustion fails candidate generation before guarding.
Compile once: reuse frozen rule-set projections and effective availability/source
outcomes, without additional mutable list reads. No authority schema changes.

Keep all DNS options and the explicit shared resolver unchanged. Query type zero
is rejected as a discriminator: the pinned matcher returns false for missing/zero
query metadata even though JSON accepts it. Removing the explicit server instead
can skip FakeIP allows and change priority. Early positive terminal routes would
bypass full ordering/private checks and change trusted observation strings.

## Verification boundary

Controlled pinned-runtime tests count upstream questions and sink connections;
retained FakeIP mapping across policy transitions must cover TCP and UDP. Static
checks cover source isolation, ordering, unchanged DNS, single source reads and
construction bounds. No external provider or host networking is required.
