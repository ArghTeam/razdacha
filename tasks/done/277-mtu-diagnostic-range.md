# Diagnose the configured client MTU (#277)

## Goal and acceptance
Honor the existing global 1280–1420 MTU range (ADR 0004). Report OK when the observed wg0 MTU equals a valid configured value, error for an invalid configured value or observed mismatch, and unknown when the interface is absent or unreadable. Diagnostics compare configuration to observations, not an end-to-end PMTU measurement. Preserve the 1280 default and configuration behavior.

Cover lower/upper edges, valid intermediate values, invalid values, mismatches and unavailable observations. Run focused API tests and lint. No deployment or network-state mutation is part of this task.

## Checkout
Primary: `/Users/romanzhigalkin/Web/razdacha`; origin: `git@github.com:ArghTeam/razdacha.git`; target: `main`; verified fetched base: `e262cc69d573a8e97a54b47cf5d700716190abf9`.


## Completion

Merged as [PR #279](https://github.com/ArghTeam/razdacha/pull/279), commit
`6ff099cc63f36a7900a9b5f0b7877da73d9b2e4b`. Independent review and focused
range/mismatch/unavailable-observation tests passed, along with all six hosted CI
checks. This fixes diagnostic classification only: no MTU, existing client configuration
or network setting changes automatically. Deployment is recorded separately from
implementation.


## Delivery

Included in the verified staging v0.11.0-rc.7 release. See the shared
[staging acceptance record](280-dashboard-design-system.md#staging-acceptance) for
exact running identity, preserved-state evidence and browser/operator boundaries.
This delivery record does not claim replay of the security/MTU test matrix on a live host.
