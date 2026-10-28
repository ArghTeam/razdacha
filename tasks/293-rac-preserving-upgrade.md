---
type: operational-validation
tracker: "#293"
---
# Preserving upgrade of the configured RAC gateway

The user authorized upgrading the configured RAC gateway without changing its
clients, policy, authentication or panel mode. This record documents the completed
operator transaction; it introduces no application changes or release publication.
The previous disposable staging target is retired and must not be contacted again.
Its earlier evidence remains historical, not current deployment authority.

## Candidate and preconditions

Preflight observed `0.11.0-rc.7`, commit `2acb9d5adbeb`, an ancestor of reviewed
source `deb5924d2362beb301443f1afa042427c3312e8e`. Schema 12, sing-box `1.12.25`,
committed v4 authority, absent bootstrap authority and no pending policy were
verified. The installation contained 11 clients, 9 rules and 8 tunnels.

The private candidate was built from an exact archive of that reviewed source,
with version `0.11.0-rc.8.rac.deb5924d2362` and embedded commit `deb5924d2362`.
Independent hashing verified the exact four-member archive and pinned inputs:

| Input | SHA-256 |
|---|---|
| Candidate archive | `fcdcb62395b6cdcda315360dfd52fe45eec8d8b1b37726e07327572f8b137f9e` |
| CLI | `3325424db7c3ade7f04f75e1dac158efd2ce01807c14df4fdc3a9a0a091b4e83` |
| Daemon | `ca9545695abd0a0f2572aac8e1e99ca8c1bcfdf5b7a617087d69801292028e03` |
| Proof helper | `205fe76b3cf1a43ce96cae488cfdb33fe15cbf5006cc08924be31d90374e0ee7` |
| Operator controller | `1913c5e4781bdb6d2fbb03dbf326b12dced66a9d4245f2b614892e1129cde1d3` |

This is a privately built candidate, not a published release or merged change.

## Execution and preservation

The operator's 29 Python tests passed, including independent review; Linux proof
tests also passed on the target with synthetic fixtures and no flow mutation.
Read-only preflight reproduced the exact actual configuration and verified pinned
runtime, TLS, DNS, Clash, WireGuard, native authority and available space.

A distinct run operation completed preflight verification, daemon stop, ingress
protection, verified full snapshot, atomic replacement, `setup --start=false`,
candidate startup and acceptance with exit zero. The ledger reached `accepted`
with `recoveryRequired=false`; the full snapshot remains in permanent private
operator storage. Protected policy, keys, authentication, password, panel mode
and all 11 clients, 9 rules and 8 tunnels were preserved. Only installed version
and permitted paired pool-inventory refresh differed. No onboarding metadata was
activated or adopted, and no client or rule was created.

## Observed result

At `2026-10-28T11:26:30Z`, external authenticated API checks matched the exact
candidate version and commit with installed-version agreement. Schema/runtime
remained unchanged, counts were 11/9/8, dirty state was false and onboarding was
not pending. Password login passed and daemon, sing-box and nginx were active.

An independent real headless browser without VPN verified the same version,
11 client rows, both aggregate traffic cards and the Setup entry. No onboarding
modal or QR opened automatically. Login was the only POST; there were zero other
writes and zero profile loads. VPN-client connectivity and media were not tested.

Running application source remains `deb5924d2362beb301443f1afa042427c3312e8e`;
later documentation commits are not the running identity and require no rebuild
or redeployment. Host values, credentials, private paths and raw policy remain
private. PR 287 remains Draft and the tracker remains open; no merge is authorized.
