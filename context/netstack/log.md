# Netstack — log

<!-- Dated entries appended by the scribe agent, newest first. -->
<!-- Schema: `## YYYY-MM-DD` then `### <ref> — <title>` with Changed / New surface / Beware. -->

## 2026-07-26

### #29 — nft-правила и проверка на стенде

**Changed:** таблица `inet razdacha` заливается одной транзакцией; проводка в демон, флаг `--reset-network`.
**Beware:** элементы интервального сета с полем `KeyEnd` ядро 6.12 отвергает с EINVAL — только пара «начало + маркер конца». Тесты с подменённым соединением этого не видят.

### #18 — wg0 через wgctrl

**Changed:** `internal/netstack/wg*.go` — интерфейс, ключи сервера, дифф пиров, статистика, клиентский конфиг.
**New surface:** `WGManager.PublicKey` подключён к хуку `api.Config.ServerPublicKey` — до поднятия интерфейса API честно отдаёт `null`.
**Beware:** `nft` показывает `reject with icmpv6` без `meta nfproto ipv6` — это нормализация вывода, а не потерянное условие.

