---
type: spec
tracker: "#286"
---
# API-backed free-start pool prototype

## Product intent

Fresh installations will eventually offer an optional free start: enable free
tunnels and starter selective-routing rules, or skip and add personal tunnels.
Country selection defaults to Auto. Media suitability and optional country
selection belong to this experience. Personal exits and personal-to-free fallback
are excluded. Existing client/rule assignments must never change implicitly.

Validate the candidate mechanism on the designated staging host before adding
installer or onboarding behavior. This increment is the first, shadow/check-only
prototype, not the completed installation experience.

## Checkout and authority

- Absolute checkout/worktree paths are recorded in the private checkout preflight.
- Remote: `origin`; intended target: `main`.
- Freshly fetched base: `61afab058acbbf02febf8adf2eb225786d4bc433`.
- Implementation branch: `feat/286-free-start-prototype`.
- Only this repository and its authorized worktree may be changed.
- Remote experiments are staging-only. Keep connection values and raw keys private.
- Staging is currently unreachable; do not substitute a production or personal host.

## First increment

1. Add the public JSON catalog driver for the official API hosts and exact API
   path. Preserve retirement of historical HTML catalog paths. Retain the current
   built-in source, disabled-on-install behavior and one-pool product policy.
2. Use one bounded batch, default 100 and at most 240 entries. Preserve request
   filters across a bounded mirror fallback; force JSON and honor cancellation,
   shared response limits and source attribution. Do not silently broaden a
   country or quality restriction when results are empty.
3. Preserve credentials and transport parameters; remove only URI fragments from
   API member identity, retaining the source remark separately as Title. A remark
   or latency change must not create a replacement node. Credential changes remain
   distinct identities. Do not infer interchangeability from host:port alone.
4. Normalize and validate structured country data. Country and existing title
   exclusions both apply conservatively. Source security claims do not override
   URI validation or the existing encryption policy. Incompatible keys and explicit
   blocked-source observations must not become eligible merely because another
   provider flag is positive.
5. Add `cmd/razdacha-pool-probe`, an explicitly invoked experimental operator tool.
   It accepts Auto or an explicit country and emits a redacted JSON report.
   Report catalog/parse/filter/config acceptance separately; never claim measured
   connectivity, throughput, UDP suitability, media success or service access.
6. Optional configuration checking uses a private temporary snapshot/config and
   `Applier.Prepare` with the installed, explicitly selected checker. Never Commit,
   Reload, start a runtime, open the installed database/configuration, use installed
   credentials, allocate ports or modify networking. Cleanup owned temporaries.
7. Add synthetic regression coverage, an experimental ADR and a staging runbook.
   All new repository content is English. Production activation and installer UI
   are deferred until actual staging evidence is reviewed.

## Acceptance and validation

- Valid/empty/oversized/truncated/malformed JSON, wrong field types and HTML bodies.
- HTTP 429/5xx, timeout/cancellation, bounded mirror fallback and preserved filters.
- Unsupported URI protocols/transports, false security metadata and parser rejection.
- Structured/title country conflicts, explicit source blocked flags, malformed metadata.
- Fragment-only changes preserve node identity; credential changes do not.
- No raw URI, UUID, password, private key or caller-supplied secret in logs/errors/report.
- No catalog requests without explicit invocation or when the existing pool is disabled.
- Check mode must not publish configuration or start/reload a service, including failures.
- Meaningful focused tests, static Linux build and repository checks pass.
- Independent code review precedes any staging invocation.
- Staging evidence must state the checked commit/binary/checker and no installed-state
  mutation. Unreachable staging remains blocked/not run, not a passing test.

## Following validation stages

A separately reviewed live harness will test a small bounded candidate set through
an isolated sidecar, including a download larger than 64 KiB and public UDP DNS/STUN.
Real provider profiles must never enter the synthetic no-uplink quality fixture.
Then validate a dedicated staging client and direct control under guarded publication,
including unavailable-pool rejection, before building the installation prompt.
