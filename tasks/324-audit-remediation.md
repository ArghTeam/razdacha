# Audit 324 verification matrix

| Finding | Implementation | Regression evidence |
|---|---|---|
| F01 | PR331 | Restored expired/revoked session generations rejected; fresh login with restored password works. |
| F02 | PR332 | Shared publication gate serializes live WG reconciliation; actual isolated Linux old-key TCP/UDP denial, sink controls and no-op handshake retention. |
| F03 | PR331 | Shared catalog/merge/generation pool filter rejects plaintext/unknown/malformed Shadowsocks ciphers. |
| F04 | PR331 | Restore guidance distinguishes preserved WG identities from reset REALITY credentials and fresh authentication. |
| F05 | PR333 | Invalid last-method/MTU requests return 400 before guards; actual durable failure after guard remains closed. |
| F06 | PR333 | Batch inventory: four SQL queries for 1/100 clients; API does not repeat per-peer eligibility rebuilds. |
| F07 | PR333 | Eligibility boolean uses shared pure checks; inventory does not serialize secret profiles. |
| F08 | PR333 | Gated fresh settings PATCH preserves omitted concurrent disable; legacy unsafe publisher refuses 503. |
| F09 | PR334 | Malformed Clash observations remain unknown; valid empty observations are measured zero. |
| F10 | PR334 | Pending/committed ownership pins and URL incarnation fencing prune unowned plain lists without resurrecting in-flight data. |
| F11 | PR336 | Pinned sing-box1.12.25 controlled TCP/UDP DNS denial, ordered positive exceptions, conservative address-dependent deferral and retained FakeIP restoration controls. |
| F12 | PR335 candidate only | Explicit accepted-policy review refreshes metadata with CAS; stale/raw retry rejected, policy/identities retained, cancel/reopen/session checks. |
| F13 | PR334 | Missing WireGuard observations render Unknown; numeric zero remains0B. |

C1 captured-DNS priority remains a separate documented design inconsistency and is not one of the 13 findings.

Main corrections are merged through `17d0dd22bbfa480f2127df7ff20be2ef91202493`; candidate-only F12 is merged through `07e933821d1ee6711e3fd8c0d4721e40644f8624`. Final integrated verification and RAC-only upgrade are pending; this record does not claim deployment. Production excluded. Runtime and isolated proofs do not establish arbitrary physical-DPI resistance or absence of Android-specific leaks.

## Integration and operational constraints

The RAC candidate retains its separate onboarding lineage and draft PR306. It is not merged into main. Conflicting maintained context sections preserve both the main restore/WG/DNS constraints and candidate onboarding recovery. These fixes introduce no runtime schema or authority-version migration.

The isolated Linux F02 proof executed source `31dfbcd17c20298276f231f10f4e31b03899669f`, test binary SHA256 `4e8609207c420f433dbcd13b421725d7afe42c790d4ee1112d44a0d85b39a9bc`. It passed without changing installed service PIDs, configuration, authority, host interfaces/default route or protected saved state. The final combined source receives its own checks before installation.

Only `193.233.112.227` is an authorized deployment target. Replace static daemon/CLI binaries, then use normal guarded startup. Preserve current clients, WG/REALITY credentials, settings, routes, tunnel definitions, password and committed policy. Verify the current healthy-only RAC configuration and authority remain byte-identical; F11 adds no preflight without shared domain rejection. Any rollback replaces verified previous binaries only; it never restores an older database or authentication snapshot. Production and DO services are outside this deployment.
