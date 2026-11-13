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
| 2 | Verify selected-exit outage, absence of direct fallback and recovery | Complete: [#348](done/348-reality-exit-reliability.md), finite owned RAC Direct/WARP evidence |
| 3a | Disambiguate retained UDP non-response with no-fault controls | Complete: [#350](done/350-retained-udp-recovery.md), bounded idle confound; long-idle control EOF and UDP non-response without an outage |
| 3b | Verify active UDP carrier recovery with matched no-fault traffic history | Next: [#352](https://github.com/ArghTeam/razdacha/issues/352); no closing hop, precise timeout or active-flow outage defect established |
| 3c | Explicitly approved reserve exits, considering an independent host | Planned after active-flow recovery characterization; current Direct/WARP share one DO host, no implicit Direct fallback |
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
phase 2 is complete with bounded selected-exit failure/recovery evidence under #348.
The bounded retained-UDP idle-confound characterization is complete. Active UDP
outage recovery, approved independent-host reserves, broader recovery and
real-client testing remain future work; the umbrella issue stays open.
Production deployment is not included in this acceptance.


## Milestone 2 acceptance and recovery limit

The [completed failure/recovery record](done/348-reality-exit-reliability.md#final-bounded-acceptance--2026-11-08)
records an accepted Direct case inside a preserved failed combined run and a later
successful WARP-only retry. Fresh HTTP/HTTPS/STUN/DNS traffic failed through the
blocked outer exit without finite WAN evidence of direct fallback; the other exit
remained healthy. Full fresh matrices recovered in about 5.71 seconds over two
attempts without Apply, application/client restart or DO configuration changes.
The independent watchdog was exercised before the holder deadline. Protected
state, checked configuration, authority, service PIDs and released guard remained.

Retained TLS requests resumed after restoration. A new STUN transaction on the
same retained WARP UDP association had no response within three seconds even though
fresh UDP worked. The later [#350 no-fault controls](done/350-retained-udp-recovery.md)
show that long idle alone closes a retained SOCKS control and produces the same
bounded UDP non-response; that is an idle confound, not proof that idle caused
the historical failure. Neither permanent failure nor seamless application
survival is established. Current two-port exits
share a DO host and provide no independent-host redundancy. Automatic failover,
independent-host reserve enrollment and broader recovery tests remain unimplemented.


## Milestone 3a acceptance and remaining active-flow question

The [completed task #350](done/350-retained-udp-recovery.md#final-bounded-acceptance--2026-11-13)
records six no-fault cases. Two independent five-/twelve-second pairs retained
matching STUN replies; thirty-/forty-five-second idle cases already had SOCKS
TCP control EOF before the post-idle request, which then timed out while fresh
Direct/WARP associations worked. Socket objects/endpoints were retained, protected
state and application PIDs were equal, and temporary clients/profiles/session were
removed. No new fault, Apply, app restart, product-code fix or deployment occurred.

This completes only the bounded idle-confound question. Nominal timeout constants
do not establish the observed closing hop or a precise threshold. The historical
#348 result remains unchanged; active-flow carrier recovery needs a continuously
active/keepalive test and matched no-fault history under [#352](https://github.com/ArghTeam/razdacha/issues/352)
before approved backup exits. No Android/media or general UDP recovery guarantee
follows from these STUN samples.
