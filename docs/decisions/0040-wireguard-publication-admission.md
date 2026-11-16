# 0040. Reconcile live WireGuard admission before publication release

Date: 2026-11-06 · Status: accepted; extends ADR 0023

## Context

Interface setup does not remove kernel WireGuard peer keys. Removing a deleted
peer's source policy and releasing its reserved address before revoking its live
key permits authenticated traffic without the former source restrictions. A
periodic database snapshot taken outside the publication gate can also reinstall
a removed key after an otherwise correct publication.

## Decision

The shared publication gate serializes the complete live peer database read and
kernel peer reconciliation, including periodic reconciliation. Guarded publication
configures the interface and reconciles current live database peers before
routing, runtime, nft, durable generation and guard release. A read or kernel
reconciliation failure retains the ingress guard and the previous reserved
addresses. Recovery uses live credentials, never peers from committed routing
records. Startup starts periodic reconciliation only after the gate exists and
recovery completes.

The existing ten-second periodic schedule still admits pending live peer changes.
It uses the existing minimal kernel diff, preserving unchanged keys and handshakes.
This does not introduce interface migration, key rotation or immediate WG deletion
outside publication. Pending deletion retains committed source restrictions until
Apply; successful Apply establishes key revocation before releasing them.

## Validation boundary

Deterministic daemon tests exercise deleted-key removal before release, failures,
committed-record recovery and a paused periodic read that must block publication.
The existing minimal-diff netstack tests verify unchanged peers cause no kernel
write. Privileged encrypted fixtures require disposable Linux namespaces and
fresh synthetic keys; ordinary unit checks do not prove external packet behavior.
