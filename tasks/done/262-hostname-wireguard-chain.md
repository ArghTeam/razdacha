# #262: Hostname WireGuard chains start without a runtime lock cycle

## Goal

Preserve WireGuard peer hostnames, DNS, keys and rule scope while making the pinned
runtime start and stop when a hostname WireGuard endpoint uses another WireGuard
endpoint as its detour. Configuration validation alone misses this startup lock cycle.

## Intended behavior

- Materialize a deterministic internal singleton selector per eligible WireGuard parent.
- Rewrite only hostname child detours; preserve addresses, keys, DNS and route targets.
- Deduplicate by parent; reject collisions with any generated endpoint/outbound tag.
- Preserve existing unavailable-chain rejection, orphan skipping and pool pruning.
- No version, database schema, dependency or deployment changes.

## Acceptance

Regression tests cover deterministic output, hostname/key/DNS/scope preservation,
singleton selectors, deduplication/collisions, literal/non-WireGuard parent exclusions,
disabled/missing chains, selected orphans and pool-member pruning. Relevant and full
Go tests plus independent review are required. Private isolated runtime investigation
proved selector indirection starts and stops while preserving hostnames; no private
production configuration is included here. No server deployment is in scope.

## Checkout

Primary: `/Users/romanzhigalkin/Web/razdacha`; remote/base: `origin/main`.
Worktree: `.codex/worktrees/fix/262-hostname-wireguard-chain`.
Parent-fetched base: `f690981dbc1dd7b0ddd08baf323c42e8512a9f08`.

## Implementation evidence

- Added the bounded hostname/WireGuard parent postprocessing pass and deferred clone
  collection until the rule is actually routed, avoiding unused startup-blocking clones.
- Added ADR 0030 and refreshed maintained singbox invariants/log.
- Passed full repository `go test ./...` and `go vet ./...`, final singbox regressions,
  and the full separate pinned-runtime suite (`go test -mod=readonly ./... -count=1`).
- Actual pinned selector cache regression proves empty/current/direct/deleted cached
  selections retain the sole parent and reject alternate selections.
- Independent source/context review found no material issue. A private frozen-snapshot
  preparation using actual implementation source `738b24c8806212e9e85e2c059ec9bd3e880b2479`
  preserved the old policy and all 25 sources (16 native, 9 plain). It generated one
  singleton alias and preserved both hostname peers, keys and DNS policy.
- The actual generated candidate started its pinned-runtime Clash API and stopped
  cleanly in an isolated network namespace without manual endpoint edits. The helper
  provenance remains the implementation commit above; this closeout changes docs only.
- Implementation acceptance is complete. Deployment and its transport-traffic gates
  are separate work; this task does not authorize a service/configuration change.
