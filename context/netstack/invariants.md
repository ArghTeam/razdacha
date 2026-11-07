---
type: invariants
title: Netstack
description: wg0, nft-правила и маршрутизация через netlink; обратимость изменений системы
---

## Invariants
- Server `wg0` and client MTU both follow `Settings.ClientMTU`: default 1280, allowed range 1280–1420 (`store.MinClientMTU` / `store.MaxClientMTU`, ADR 0004). `WGConfigFromSettings` propagates this value to the server interface.
- Только netlink: `wgctrl`, `google/nftables`, `vishvananda/netlink`. Ни `wg`, ни `wg-quick`, ни `nft`, ни `ip` бинарниками.
- Интервальные сеты задаются парой элементов — начало и адрес, следующий за концом, с `IntervalEnd`. Поле `KeyEnd` живое ядро 6.12 отвергает с EINVAL, и это не видно в тестах с подменённым соединением.
- Подсети сливаются через `netipx` до заливки: пересекающиеся интервалы ядро отвергает всей транзакцией, а `auto-merge` есть только в userspace-утилите.
- Правила уходят одним пакетом изменений и одним `Flush` — иначе появляется окно, в котором трафик идёт мимо туннеля.
- Существование таблицы проверяется до формирования пакета: удаление отсутствующей таблицы отменяет весь пакет вместе с созданием новой.
- Синхронизация пиров идёт диффом; совпадающие пиры не переписываются, при пустом диффе `ConfigureDevice` не вызывается — живым клиентам не рвутся хендшейки.
- Приватный ключ сервера лежит в `settings` вне структуры `Settings`: сохранение настроек из UI его не затирает и наружу он не отдаётся.
- Routing rules and the separate `inet razdacha_guard` table survive daemon exit. Startup installs the early wg0 ingress DROP guard before WG setup, and releases it only after a checked, durably committed generation. Normal direct routing resumes after recovery; daemon restart can interrupt clients. Explicit reset/uninstall removes those WG routing tables (ADR 0023); the separate public ingress guard has the lifecycle restrictions below (ADR 0032).

- Candidate WG address, listen port and MTU are validated before publication and reconciled under the guard before route/runtime/nft. Temporary managers use live key storage and reconcile current live peer rows, never committed authentication; the long-lived peer-sync/stat manager retains its fixed interface identity. Startup uses trusted committed/seed WG settings; only genuine legacy absence allows guarded DB initialization for administrative access. All stored peers, including disabled peers, must fit usable pool addresses without server collision.
- Explicit unchanged Apply guards and repairs WG/route/nft without restarting a known runtime. Healthy background no-ops may skip mutation.
- Guard and routing publications use fresh netlink connections per attempt so a rejected batch cannot contaminate a later transaction. The ingress guard is a separate early-prerouting DROP table, covering forwarded traffic, DNS and tproxy; SSH/public-panel traffic does not enter through wg0. No cross-process atomic-transaction claim is made (ADR 0023).
- Private routing-generation records belong to `internal/networkstate`; they preserve policy/list content, never restore live peer/authentication database rows, and reject symlinks, non-owner files, non-0600 permissions and malformed envelopes. Interrupted publication/recovery leaves the kernel guard installed.

## Key entry points
- `internal/netstack/wg.go` — `WGManager`: `Up`, `SyncPeers`, `Stats`, `PublicKey`.
- `internal/netstack/nft_rules.go` — чистая сборка набора правил, тестируется без root.
- `internal/netstack/nft.go` — перевод набора в выражения nftables, только Linux.
- `cmd/razdachad/netfilter_linux.go` — проводка в демон и `--reset-network`.

- `internal/networkstate/state.go` — private committed/bootstrap authority and durable restore invalidation.

- Supported native original routing, exact domain-only DNS and finite IPv4 nft projection share one accepted AST generation (ADR 0025). Active watched-blob corruption installs the guard and marks runtime uncertain, including when a pending candidate removes that source; rejected new input alone retains healthy last-good service. Existing DNS-port/local-address nft exemptions remain boundaries. No implicit catch-all interception or autonomous native refresh is allowed.

- Whole-Internet interception uses exact committed IPv4 peer sources, not a global destination catch-all. DNS, configured gateway and canonical local destinations precede source TCP/UDP capture; source return prevents unsupported protocols entering ordinary marks. Residual FORWARD traffic from selected sources is rejected without established-flow exemptions. Gateway INPUT remains available, private forwarding and unsupported protocols fail closed, other peers retain behavior (ADR 0028).

- ADR 0029 transport envelopes use the separate `transport_subnets` nft set and TCP/UDP-only marks after DNS/local exemptions. Ordinary subnet behavior remains unchanged; transport-only lists never newly mark ICMP or arbitrary IP protocols. Generation v4 pins these fields; older envelopes cannot carry transport projection authority.

## Encrypted connection-quality test boundary

- The double-opt-in [quality fixture](../../docs/12-connection-quality.md) requires
  fresh network and mount namespaces before mutation, uses disposable kernel
  WireGuard keys and a documentation-only underlay without a host uplink, and
  hides installed runtime state and userspace WireGuard sockets with private tmpfs.
- Handshake and increasing bidirectional WireGuard counters prove actual encrypted
  ingress. Existing production publication, nft/TProxy and independent sink/drop
  checks exercise selective/full/shared policy. Synthetic SOCKS exits do not
  prove a real outgoing provider, external path MTU or Google Meet/WebRTC success.
- Quality capture defrag verifies complete datagrams; zero socket drops and a
  barrier do not prove absence of arbitrary incomplete fragments held/dropped by
  kernel reassembly. Negative fail-closed probes remain small and unfragmented.
- Address/MTU publication still uses the test WG adapter; the fixture does not
  establish production key allocation or live peer-reconciliation lifecycle.
  Its two logical source identities share one synthetic encrypted peer.
- The additional `RAZDACHA_WIRE_REALITY_COEXIST=1` exercise requires both existing
  quality opt-ins. It checks concurrent authenticated REALITY TCP/UDP/DNS while
  that real kernel WG peer retains its key and advances bidirectional encrypted
  counters. The profile-derived wrapper binds only its synthetic `under1`; it is
  not a full-TUN, systemd, provider or production WG-key-lifecycle proof. The
  [coexistence record](../../tasks/297-wg-reality-coexistence.md) pins its execution
  and preserves the existing WG address/MTU publication-adapter limitation.

- The separate `RAZDACHA_WIRE_WG_ADMISSION=1` case requires both quality
  opt-ins and the same fresh namespace checks. It replaces only its disposable
  client identity with a fresh DB key, then uses production `Up` and live peer
  reconciliation. It asserts removal before ingress release, old-key TCP/UDP
  denial with independent sink controls and unchanged periodic handshakes. It
  does not extend the ordinary quality/coexistence case's two-source adapter
  evidence. Execution conditions and results belong to
  [task 325](../../tasks/done/325-wg-publication-admission.md).

## Public client safety boundary (ADR 0032)

- WireGuard peer synchronization excludes `WireGuardDisabled` peers without changing keys/addresses. The entire periodic live-row read and kernel diff shares the publication gate. Guarded publication reconciles live kernel admission before route/runtime/nft, durable authority and guard release; read/sync failure retains the guard and committed address reservations. Startup starts the ten-second periodic loop only after recovery. Committed policy never supplies authentication (ADR 0040).
- Reality uses the separately owned early inet INPUT TCP guard, covering established traffic and the union of previous/candidate listener endpoints. It does not change forwarded outgoing-tunnel traffic or rely on wg0 prerouting checks. The daemon guards saved public listeners before network startup.
- A private versioned safety manifest approves exact checked config hashes and listener coverage. Publication uses short shared locks to stage manifest+kernel guard, releases the lock before runtime restart, commits policy and discards bootstrap authority, then releases guards last. Helper starts cannot resurrect a stale public config without valid manifest/hash/coverage.
- Any uncertainty after guarding leaves both client paths closed. Active native-artifact corruption also closes public ingress. Explicit reset retains the public manifest/barrier; uninstall requires verified WG-only state first.

- After checked restart, release rereads controlled private config bytes under the safety lock and requires exact candidate equality before narrowing manifest coverage or releasing either WG/public barrier. A pre-start hook restoring previously approved bytes retains union authority and both closed paths. A failed release recloses both guards.

- Client connection evidence reads the existing WG peer-stat source without kernel mutation. Gateway receive/transmit are encrypted peer aggregates; missing, reset, future-handshake or unsafe counters do not prove inactivity, application throughput, client latency or a site's success. The API owns bounded source coalescing and private before/after publication checks (ADR 0043).

## Owned RAC exit outage fixture

- The opt-in [RAC exit fixture](../../tools/rac-egress-fault/README.md) is separate
  test code, not daemon routing or automatic failover. It requires an isolated
  kernel roundtrip before live use, restricts host/endpoint identity, uses one
  unpredictable standalone exact IPv4/TCP OUTPUT counter/DROP table and refuses
  foreign ownership/schema. No application table/configuration is edited.
- A verified independent monotonic watchdog precedes installation. Durable
  revocation and kernel operations share a private bounded lock; deadlines are
  checked immediately before and after exclusive installation. Owned fault unit
  lifetime/signal cleanup complement the watchdog. Journals remain as tombstones;
  revocation/status stay bound to the prepared boot/network namespace;
  cleanup remains available without the RAC interface address, and persistence/unit
  failures still attempt exact owned removal while reporting incomplete revocation.
  Only verified table absence permits watchdog cancellation. Evidence and limits
  are tracked separately in [task #348](../../tasks/done/348-reality-exit-reliability.md).

- The completed RC16 fixture evidence is finite: owned Direct/WARP outer-leg
  blackouts rejected fresh test traffic with no fixed-target direct WAN packets,
  and fresh matrices recovered without application restart. It does not establish
  inner-WARP failure behavior or independent-host redundancy. A retained WARP UDP
  association had no new-transaction reply within three seconds after restoration
  despite fresh UDP recovery; seamless retained-flow recovery is not a contract.
