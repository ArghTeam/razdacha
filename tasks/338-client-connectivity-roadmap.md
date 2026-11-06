# Client connectivity roadmap

Tracker: [#338](https://github.com/ArghTeam/razdacha/issues/338). This is an open,
incremental roadmap inspired by the 3x-ui comparison. Existing client identities,
credentials, routing policy, guarded publication and explicit Apply remain constraints.
Production is excluded from the first delivery; deployment acceptance is RAC-only.

## Phases and status

| Phase | Scope | Status |
| --- | --- | --- |
| 1a | Client-scoped applied web-policy explanations, separate from observations and actual client traffic | Complete: PR #340 merged and controlled RAC acceptance passed |
| 1b | Scoped quality observations and client-driven connection evidence | Next, planned; requires its own specification and review |
| 2 | Revocable, scoped client subscriptions and compatible-client updates, with independent live admission | Planned; not implemented by #339 |
| 3 | Remote exit observation/management for explicitly enrolled servers from one panel | Planned; no implicit discovery or production mutation |
| 4 | Approved exit groups with explicit failover policy, bounded health observations and no implicit Direct fallback | Planned; not implemented by #339 |
| 5 | Pinned AmneziaWG and Hysteria2 experiments with compatible clients and comparative loss/latency/UDP tests | Planned; existing [#163](https://github.com/ArghTeam/razdacha/issues/163) remains separate |

Phase 1 is split into 1a and 1b; the five-phase roadmap remains open after 1a.
Later phases need concrete specifications, acceptance criteria and independent review
before implementation. No blanket DPI resistance or connection-quality guarantee
is implied by the roadmap.

## First delivery boundary

[#339](done/339-client-route-explanations.md) records conditional domain-bearing
web TCP/443 routing and gateway A-query policy from committed authority. It never
infers a client path from global DNS, editable policy or a generic HTTPS probe.
[ADR 0042](../docs/decisions/0042-client-applied-policy-explanations.md) records the
precise source, uncertainty, privacy, ordering and UI lifetime contracts.

First-delivery verification distinguishes deterministic/synthetic checks, read-only
staging inspection and a controlled RAC deployment. Prepared artifacts alone do
not establish deployed acceptance. Existing clients/keys/rules/tunnels must be
preserved, and cloned or archived production tunnel credentials are not test inputs.

## Milestone 1a acceptance

The [completed task record](done/339-client-route-explanations.md#final-acceptance--2026-11-07)
records the RC15 source/artifact provenance, successful source and combined CI, local
Go/race/browser checks, preserved existing RAC state and actual served API/UI proof.
The running candidate is `0.11.0-rc.15.rac.b5b8e153cdd1`. This closes only 1a;
phase 1b and phases 2–5 remain future work, and the umbrella issue stays open.
No client-path or quality result is inferred from the read-only policy explanation.
