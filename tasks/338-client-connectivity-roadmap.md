# Client connectivity roadmap

Tracker: [#338](https://github.com/ArghTeam/razdacha/issues/338). This is an open,
incremental roadmap inspired by the 3x-ui comparison. Existing client identities,
credentials, routing policy, guarded publication and explicit Apply remain constraints.
Production is excluded from the first delivery; deployment acceptance is RAC-only.

## Product goal and exclusions

The goal is stable Internet access for family and friends, with little manual
maintenance. Profile subscriptions, billing, tariffs, quotas, paid access and
commercial distribution are outside this roadmap. Remote fleet management is
not a prerequisite. Existing identities, explicit Apply and fail-closed selection
remain constraints; stored health never authorizes Direct fallback.

## Phases and status

| Phase | Scope | Status |
| --- | --- | --- |
| 1a | Client-scoped applied web-policy explanations | Complete: PR #340 and controlled RAC acceptance |
| 1b | Scoped client activity and independent exit evidence | Complete: PRs #346/#345 and controlled RAC acceptance |
| 2 | Verify selected-exit outage, absence of direct fallback and recovery | In progress: [#348](348-reality-exit-reliability.md), existing owned RAC REALITY clients only |
| 3 | Explicitly approved reserve exits with bounded health and no implicit Direct fallback | Planned; separate specification after phase 2 evidence |
| 4 | Recovery after network interruption, daemon/runtime failure and reboot | Planned; preserve committed policy and independent live admission |
| 5 | Real Android/router clients, DNS, UDP and long connections under failure/recovery | Planned; compatible-client and finite-path evidence required |

New transports, including the existing [#163](https://github.com/ArghTeam/razdacha/issues/163)
AmneziaWG work and Hysteria2 experiments, are optional later investigations only
when measured availability needs justify them. They are not promised improvements
or blanket DPI/connection-quality guarantees. Later phases require concrete
specifications and independent review before implementation.

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
The accepted 1a candidate was `0.11.0-rc.15.rac.b5b8e153cdd1`. That milestone
closed only 1a; its read-only policy boundary remains unchanged by later work.
No client-path or quality result is inferred from the read-only policy explanation.


## Milestone 1b acceptance

The [completed Connection evidence task](done/342-client-connection-evidence.md#final-acceptance--2026-11-07)
records merged backend/UI delivery, exact-source review, complete combined checks,
RC16 artifact provenance, preserved RAC state and actual served API/UI acceptance.
RAC runs `0.11.0-rc.16.rac.a945231fbf78`. Existing owned DO Direct and DO WARP
REALITY profiles carried controlled HTTPS and UDP STUN traffic without local TUN
or server tunnel reconfiguration. Ingress observations and independent applied-exit
HTTPS remain separate; this is not selected-site or general quality proof.

Phase 1 is complete. The user excluded subscriptions and commercial features;
phase 2 now verifies selected-exit failure/recovery under #348. Approved reserves,
recovery and real-client testing remain future work; the umbrella issue stays open.
Production deployment is not included in this acceptance.
