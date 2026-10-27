# 0033. Opt-in fresh-install free-start onboarding

Date: 2026-10-27. Status: experimental, authorized for staging; supersedes the
installer deferral in ADR 0032 and clarifies ADR 0014/0018 for explicit setup intent.

## Context

The user authorized an actual fresh installation to test onboarding. A catalog
probe alone cannot establish the proposed install-and-use experience. Existing
installations and personal tunnels must remain isolated from this choice.

## Decision

Offer a fresh-only CLI choice through `/dev/tty`, with Skip as the default and
Auto as the default country. No terminal or EOF skips. Explicit setup flags or
environment support unattended opt-in; upgrades never prompt or change pool/rules.
Explain that free exits are third-party and have no usability guarantee.

An explicit opt-in enables the single built-in pool at the new API and creates
one selective starter rule scoped only to the first peer, using the maintained
preset without its Google AI entry. This is explicit initial intent under ADR
0014, not a change to ordinary preset behavior or a new pool topology. Skip keeps
ADR 0018's ordinary disabled igareck default and performs no source requests.

Before any services start, synchronously acquire every starter source with the
existing bounded Fetcher and immutable native authority in the canonical cache.
Freeze that exact source map for both initial checked configuration generation
and bootstrap generation v4. Existing guarded daemon recovery verifies and
publishes it. Missing source contents never produce a permissive empty-list seed.
Source acquisition or configuration failure aborts before service startup/seed.
Catalog failure keeps the selected-resource policy with an empty unavailable
pool, so it rejects rather than silently routing directly.

## Consequences

Only the installer deferral of ADR 0032 is superseded; its API admission, redaction,
bounded requests and shadow-probe guarantees remain. No production default is
changed, no additional pool or personal fallback is introduced, and no live
sidecar is added. Installation and config admission do not prove actual service,
video, call, TCP or UDP usability; staging evidence remains required.
