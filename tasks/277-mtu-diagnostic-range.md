# Diagnose the configured client MTU (#277)

## Goal and acceptance
Honor the existing global 1280–1420 MTU range (ADR 0004). Report OK when the observed wg0 MTU equals a valid configured value, error for an invalid configured value or observed mismatch, and unknown when the interface is absent or unreadable. Diagnostics compare configuration to observations, not an end-to-end PMTU measurement. Preserve the 1280 default and configuration behavior.

Cover lower/upper edges, valid intermediate values, invalid values, mismatches and unavailable observations. Run focused API tests and lint. No deployment or network-state mutation is part of this task.

## Checkout
Primary: `/Users/romanzhigalkin/Web/razdacha`; origin: `git@github.com:ArghTeam/razdacha.git`; target: `main`; verified fetched base: `e262cc69d573a8e97a54b47cf5d700716190abf9`.
