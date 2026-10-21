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
- Routing rules and the separate `inet razdacha_guard` table survive daemon exit. Startup installs the early wg0 ingress DROP guard before WG setup, and releases it only after a checked, durably committed generation. Normal direct routing resumes after recovery; daemon restart can interrupt clients. Explicit reset/uninstall removes both tables (ADR 0023).

- Candidate WG address, listen port and MTU are validated before publication and reconciled under the guard before route/runtime/nft. Temporary managers use live key storage and never replay peers; the long-lived peer-sync/stat manager retains its fixed interface identity. Startup uses trusted committed/seed WG settings; only genuine legacy absence allows guarded DB initialization for administrative access. All stored peers, including disabled peers, must fit usable pool addresses without server collision.
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
- Address/MTU publication still uses the test WG adapter; the fixture does not
  establish production key allocation or live peer-reconciliation lifecycle.
  Its two logical source identities share one synthetic encrypted peer.
