# Public ingress safety primitives

This package supplies non-secret startup authority for the optional authenticated
client ingress described by ADR 0032. It is dormant until the daemon, runtime
service and policy compiler explicitly wire it. It does not create a listener,
load credentials, access the database, start a service or change a tunnel.

## Short publication transactions

`Manifest` version 1 contains exact checked configuration SHA-256 values, the
union of old/candidate TCP endpoints, and durable `Guarded` intent. Addresses are
IP literals; an empty address normalizes to `::`, conservatively covering both
IPv4 and IPv6. `0.0.0.0` covers IPv4 only. Endpoint-specific rules never broaden
to other addresses or ports.

`WithLock(manifestPath, operation)` protects a short cross-process transaction.
The controller must hold it while expanding/persisting authority and installing
the matching kernel barrier, and while releasing the barrier/finalizing status.
It must release the lock **before** a runtime restart: the startup helper takes
the same lock. `Read` and `Write` deliberately do not acquire it recursively.
The sibling lock file is retained to preserve one lock inode for waiting callers.

`Startup(configPath, manifestPath, install)` locks the authority read and kernel
callback together, preventing an old helper from installing stale endpoint coverage
after the controller expands it. It verifies exact private config bytes and complete
endpoint coverage, persists `Guarded=true`, and only then calls the installer. A
failed installer retains guarded intent. Guarded intent is not kernel attestation;
the controller must also retain uncertainty and close ingress on release failure.

Missing authority is accepted only for the checked legacy listener shape: exact
`dns-in` direct DNS on port 53 bound to an explicit unicast IPv4 address, plus
loopback `tproxy-in` and loopback controllers. DNS preserves the existing WG address
contract, including CGNAT and public ranges: the helper uses owner-controlled config
without querying a live `wg0` or DB before startup. Wildcards, multicast, limited
broadcast, zones, non-IPv4 DNS and unknown names are refused. Unknown inbounds and
public TProxy/controller listeners remain refused. A
legacy startup does not invoke the installer or clear a pre-existing barrier.
Supported VLESS listeners require TLS and REALITY and always require authority.
This inspection complements, rather than replaces, the pinned runtime's full check.

## Storage and kernel boundaries

Authority and lock parents must be owner-controlled mode 0700. Configuration parents
may be mode 0755 when owner-controlled and not group/world writable, preserving the
existing runtime layout. Every file is a regular owner-controlled mode 0600 file;
symlinks and unsafe ancestors are rejected. Callers use clean absolute paths. On
platforms with standard symlink aliases such as macOS `/var`, resolve the trusted
directory before constructing these paths. Manifest writes use synced temporary
files, atomic rename and directory sync. JSON duplicates, unknown authority fields,
missing fields, unsupported versions and resource-budget violations are refused.

`netstack.SetPublicClientGuard` uses a separate `inet razdacha_public_guard` table
and an early INPUT hook. Only locally delivered TCP packets for admitted endpoints
are dropped, including established flows. Forwarded traffic and unrelated local
ports are outside this hook/matcher. Each mutation uses a fresh netlink connection
and one atomic batch; malformed endpoints are rejected before kernel access. The
existing WG barrier and routing table are not replaced. The caller uses `WithLock`
to coordinate controller/helper kernel writes. Non-Linux calls fail explicitly.

## Verification

Normal package tests cover strict authority/permissions, atomic replacement,
configuration closure, exact hashes, endpoint coverage, failed startup and lock
serialization. The opt-in Linux fixture self-executes into fresh network and mount
namespaces, verifies only loopback exists, and changes no host network state:

```sh
RAZDACHA_PUBLIC_GUARD_TEST=1 go test ./internal/netstack \
  -run '^TestPublicClientGuardWire$' -count=1 -v -timeout=30s
```

It requires root and namespace capability. It checks new/established connections,
old/new endpoint transitions, unrelated listeners, invalid mutation preservation,
address scope and release. IPv6 wire checks explicitly skip if IPv6 loopback is
unavailable. This fixture is not a real provider, external client or DPI test and
does not establish daemon publication, boot integration or authentication recovery.


On 2026-11-04 the primary operator's reviewed isolated Linux execution passed,
including IPv4 and IPv6 checks, in 1.69 seconds. Existing service main PIDs and
configuration/runtime hashes stayed unchanged; the owned test cgroup was removed.
The [foundation record](../../tasks/297-ingress-safety-foundation.md) pins the
source/executable identity and scope. This is bounded kernel-barrier evidence, not
live application Apply, boot integration, authentication or REALITY-client proof.
