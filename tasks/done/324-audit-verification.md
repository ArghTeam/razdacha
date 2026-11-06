# 324 — Audit remediation verification

Date: 2026-11-06. Tracker: [audit umbrella #324](https://github.com/ArghTeam/razdacha/issues/324).
This record closes the thirteen confirmed findings from the independent audit.
It separates merged implementation, finite synthetic proof, candidate-only work
and the RAC deployment result. Production is excluded.

## Source and repository boundaries

The main fixes merged through PRs 331, 332, 333, 334 and 336. The verified functional
main source is `17d0dd22bbfa480f2127df7ff20be2ef91202493`.
Onboarding F12 merged through PR335 only into `epic/297-rac-reality-integration`;
it is not a main feature. The final candidate source is
`c1e8ef1a6e6cfbdfe74dcba222092383b8041c2e`.
[PR306](https://github.com/ArghTeam/razdacha/pull/306) remains open and draft;
this work does not authorize its merge into main.

Documentation checkout: `.codex/worktrees/docs-324-audit-verification`, branch
`docs/324-audit-verification`. Designated primary checkout:
`/Users/romanzhigalkin/Web/razdacha`; remote/base: `origin/main`, freshly fetched at
`17d0dd22bbfa480f2127df7ff20be2ef91202493`. User edits in the primary checkout
remain outside the documentation change. The five completed specifications below
retain their original content and verification boundaries after relocation. The
only source-file change in this closure is a comment clarifying that request-local
REALITY eligibility metadata still contains WG private key material in peer rows
and must never be serialized; executable behavior is unchanged.

## Finding disposition

| Finding | Fix and scope | Verification boundary |
|---|---|---|
| F01 — restored revoked sessions | [PR331](https://github.com/ArghTeam/razdacha/pull/331), [task 326](326-restore-pool-safety.md): clear sessions and rotate authentication generation in the staged restore database. | Synthetic live/revoked/expired session and stale-generation cases; restored password permits fresh login; WG keys remain preserved. |
| F02 — deleted WG key survives publication | [PR332](https://github.com/ArghTeam/razdacha/pull/332), [task 325](325-wg-publication-admission.md): live WG reconciliation joins guarded publication and periodic synchronization shares its gate. | Deterministic ordering/failure/race checks and final isolated encrypted TCP/UDP fixture described below. |
| F03 — plaintext automatic pool members | [PR331](https://github.com/ArghTeam/razdacha/pull/331), [task 326](326-restore-pool-safety.md): shared catalog/merge/generation filtering rejects plaintext, unknown and malformed Shadowsocks methods. | Plain/base64 encrypted and invalid synthetic links; manual parsing compatibility retained. |
| F04 — misleading restore guidance | [PR331](https://github.com/ArghTeam/razdacha/pull/331), [task 326](326-restore-pool-safety.md): distinguish reusable WG identities from reset REALITY credentials. | Guidance requires fresh login, REALITY reconfiguration/profile reissue and explicit Apply; endpoint changes remain a separate reason to update profiles. |
| F05 — invalid revocation guards ingress | [PR333](https://github.com/ArghTeam/razdacha/pull/333), [task 327](327-reality-control-coherence.md): validate merged intent before installing guards. | Authenticated invalid peer/global disable returns 400 without guard/auth changes; later durable-write failure still remains closed. |
| F06 — quadratic inventory admissions | [PR333](https://github.com/ArghTeam/razdacha/pull/333), [task 327](327-reality-control-coherence.md): one coherent indexed metadata snapshot. | Four instrumented SQL reads at both 1 and 100 clients; no wall-clock latency claim. |
| F07 — profiles rendered for booleans | [PR333](https://github.com/ArghTeam/razdacha/pull/333), [task 327](327-reality-control-coherence.md): metadata eligibility shares pure delivery checks. | Inventory does not serialize URI/JSON secrets; pending/active/stale/disabled/guarded eligibility regressions. |
| F08 — partial settings lost updates | [PR333](https://github.com/ArghTeam/razdacha/pull/333), [task 327](327-reality-control-coherence.md): merge supplied fields against fresh state inside the publication gate. | Controlled concurrent saves preserve omitted settings and global disable; unsafe legacy publishers refuse PATCH. |
| F09 — malformed observations become zero | [PR334](https://github.com/ArghTeam/razdacha/pull/334), [task 328](328-observation-list-retention.md): classify controller rows before filtering. | Malformed/unclassifiable snapshots remain unknown; valid empty snapshots observe zero. |
| F10 — obsolete plain-list memory retention | [PR334](https://github.com/ArghTeam/razdacha/pull/334), [task 328](328-observation-list-retention.md): pending/committed ownership pins and source incarnation fences. | Released sources are pruned; late in-flight reads cannot resurrect them; failed Apply preserves committed consumers. |
| F11 — denied REALITY hostname leaks upstream | [PR336](https://github.com/ArghTeam/razdacha/pull/336), [task 329](329-reality-dns-denial.md): bounded reject preflight before shared resolution. | Pinned 1.12.25 cold TCP/UDP DNS counters, sink controls, earlier positives, conservative address-dependent deferral and retained FakeIP controls; see ADR0041. |
| F12 — prepared onboarding dead end | [PR335](https://github.com/ArghTeam/razdacha/pull/335), candidate only; issue #330 and its candidate specification remain outside main. | Explicit accepted-policy preview and metadata CAS preserve policy/identities. Raw/stale retries reject; browser tests cover cancel/reopen, modal/session lifetime and failed confirmation after previous Apply. |
| F13 — absent WG samples shown as zero | [PR334](https://github.com/ArghTeam/razdacha/pull/334), [task 328](328-observation-list-retention.md), main parity with the prior candidate. | Actual embedded UI shows missing observations as Unknown and numeric zero as 0 B. |

## Integrated checks and isolated proof

The final candidate passed the full Go suite, focused race suites for daemon, API,
store, lists, Clash and sing-box, Linux lint, and pinned sing-box runtime tests.
[CI run 42224969126](https://github.com/ArghTeam/razdacha/actions/runs/42224969126)
passed test, UI browser, amd64/arm64 build, sing-box check and lint jobs at the exact
candidate source above. The individual completed specifications retain their
narrower local verification records; relocating them does not broaden those claims.

The final isolated encrypted WireGuard fixture used test binary SHA-256
`b80d50d79e8c50ed3578213e7499e82ac2c40f411fd6352b7fa2e75efc298019`
built from `c1e8ef1a6e6cfbdfe74dcba222092383b8041c2e`. It passed in 3.184 seconds,
including capture barriers without drops, independent sink controls, removal of
the deleted live key before guard release, old-key TCP/UDP denial to selected and
ordinary destinations, and retained no-op periodic handshakes. Installed state and
service PIDs were preserved. The fixture used fresh synthetic credentials and
isolated namespaces without changing installed client identities.

## RC14 artifact attestation

Frozen version: `0.11.0-rc.14.rac.c1e8ef1a6e6c`. Both Linux amd64 and arm64 daemon/CLI
artifacts are statically linked with `CGO_ENABLED=0`, VCS revision equal to the
candidate source and `vcs.modified=false`. Daemon linker metadata contains the full
source SHA and expected version; a local synthetic authenticated API probe and CLI
version probe verify these values independently. The arm64 binaries were built,
not executed on arm64.

The frozen Linux amd64 daemon SHA-256 is
`5d4a4e4883f16d84d14a2ac26f68f4969764f6cddcece1cd7eb65f9828130432`;
the CLI SHA-256 is
`41794202a0729b476b1ff6d30a7c431bacbe2fafd54b00336d9e4ef877c59b92`.
Artifact attestation alone is not deployment evidence.

## RAC deployment acceptance

The primary agent confirmed successful RAC-only installation of
`0.11.0-rc.14.rac.c1e8ef1a6e6c` from candidate source
`c1e8ef1a6e6cfbdfe74dcba222092383b8041c2e`. Normal guarded startup completed;
daemon, sing-box and nginx services were active. Saved state and credentials,
checked sing-box configuration and committed authority retained their exact
pre-upgrade fingerprints/hashes. The recovery backup is private at
`/var/backups/razdacha/audit-324-20261106T132855Z`. No setup/reset or database
restore occurred; the upgrade's recovery policy replaces only binaries.

Final authenticated verification reported schema 13, four saved clients and
installed metadata matching the running version with no mismatch. After the
traffic/UI proof, an explicit compare-and-swap updated only `installed_version`;
all other protected rows remained unchanged. This bookkeeping write is separate
from the exact state preservation established during the binary upgrade.

| Actual client check | Observed HTTPS exit | UDP and DNS |
|---|---|---|
| Direct | `165.22.196.130`, WARP off, NL/AMS | UDP STUN and gateway DNS A passed. |
| WARP | `104.28.219.140`, WARP on, NL/AMS | UDP STUN and gateway DNS A passed. |

The API exposed live open-flow byte observations. Read-only checks of the actual
served panel verified four client cards, two live clients and the mobile layout.
All owned proof processes, temporary profile copies and administrative proof
sessions were cleaned up. Production was untouched. These are bounded observations
for the tested Direct/WARP clients, not a claim about every protocol or provider.
The private final proof records the exact candidate source and successful traffic
and live UI checks; credentials and profile contents are not included here.

## Remaining boundaries

Captured-DNS priority inconsistency C1 is a separate documented design question,
not one of the thirteen confirmed defects. Its behavior is not silently changed
by the REALITY preflight fix. These finite proofs do not establish universal DNS
confidentiality, Android compatibility, physical DPI resistance, working external
providers, media/UDP quality or general absence of leakage. No production changes
or candidate-to-main feature promotion are included.
