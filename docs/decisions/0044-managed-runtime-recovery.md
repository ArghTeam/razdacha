# ADR 0044: Recover managed independent runtime restarts

Status: accepted
Date: 2026-11-18

Extends ADRs 0023 and 0032. Independent sing-box startup closes public ingress
before binding. Previously this could retain a closed barrier until manual repair.

The managed runtime pre-start helper records an optional `runtime_restart` marker
only after its kernel guard succeeds, only for public endpoints, and only if the
previous manifest was released or already marked. Generic daemon startup, restore
and intentional emergency guards clear this marker. Old manifests remain readable.
Older binaries reject the transient new field and remain closed.

A five-second daemon watcher serializes repair through the publication mutex.
Only initialized, certain state with marked guarded intent qualifies. Exact current
config hash and endpoint coverage must match the manifest, and private persisted
committed policy/lists must equal the in-memory authority. The full checked guarded
publication uses committed rules/lists and current live authentication. It restarts
the runtime, waits up to five seconds for the local controller Version endpoint,
and releases guards last. This readiness check is scoped to automatic repair. Pending edits never become applied policy.
A failed repair sets uncertainty, retaining explicit administrator recovery and
preventing repeated runtime restart attempts. No manifest lock spans restart.

Acceptance distinguishes deterministic regression tests from staging crash tests.
This decision grants no broader recovery or connection continuity guarantee.
