# 235. Bounded native transport compatibility

Implement ADR 0029 so existing bounded UDP/destination-port native lists can be
published alongside the merged whole-Internet client routing feature. Preserve exact
original route bytes, domain-only DNS, separate transport nft interception, last-good
refresh, versioned verification and guarded recovery. Reject unsupported context.

Provenance: primary /Users/romanzhigalkin/Web/razdacha; remote origin
(git@github.com:ArghTeam/razdacha.git), target main; freshly fetched base
182974b0b23360ae2da854693cb43a9a4af5b037. Implementation checkout:
.codex/worktrees/fix-235-native-transport, branch fix/235-native-transport.

Validation: JSON/SRS equivalence, positive boolean algebra, invalid inputs and work
budgets, cache/version/recovery tests, nft TCP/UDP isolation and pinned-runtime tests.


## Verification record

The complete local `CGO_ENABLED=0 go test ./...`, `CGO_ENABLED=0 go vet ./...`
and pinned `tests/dnsruntime` suites passed. JSON/SRS unit coverage includes positive
transport forms, mixed-AND unconditional address preservation, unsupported context,
unbounded combined coverage, rejected-refresh validators, snapshot isolation,
legacy authority recovery and active projection tampering.

The controlled Linux runtime matrix passed against source
`5e4995a389b526693263c805ccb11dad9222804f` using static test ELF SHA-256
`78b629ba0628b31e543751dfd41f8d6c6d63b9b89a13530609cd4a6dffedc20b`.
Build: `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./cmd/razdachad`.
The runtime was pinned to sing-box 1.12.25. JSON completed in 3.610 seconds and SRS
in 3.662 seconds; both returned success. The matrix exercised enabled-exit UDP
delivery with a nonce-matched synthetic relay receipt, wrong protocol/address/port
controls, peer scope, ordered direct exceptions, raw protocol isolation, domain-only
DNS rejection, unavailable-target rejection, client-wide exit precedence and
committed recovery. Twenty-four receiver barriers acknowledged zero reported drops.

Private execution receipt SHA-256:
`5c451c290daa2a83ad1565a63efa50123894bb6e6aa24e59102207fe3494f6f7`.
Installed services, runtime configuration, database schema, peer-identity HMAC and
binary hashes remained unchanged. The owned execution directory was removed.

This certifies finite observations in isolated network/mount namespaces using a
veth client adapter and synthetic SOCKS exit. It does not certify an encrypted
WireGuard handshake, a provider tunnel, lossless capture, deployment or universal
traffic behavior. Independent evidence review and final CI remain release gates.
