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
