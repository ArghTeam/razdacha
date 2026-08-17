# Атрибуция

## Podkop

Архитектура razdacha основана на [Podkop](https://github.com/itdoginfo/podkop)
(© itdoginfo, GPL-2.0-or-later) — маршрутизаторе трафика для OpenWrt.

Из Podkop взяты: модель конфигурации (направления маршрутизации, типы подключения,
источники списков), механика селективной маршрутизации через FakeIP + nftables,
логика разбора proxy-URL в outbound-конфигурацию sing-box, структура диагностики.
Разбор с точными ссылками на исходные файлы — в
[docs/research/podkop-analysis.md](docs/research/podkop-analysis.md), каждое решение
в [docs/decisions/](docs/decisions/) содержит секцию «Источник в Podkop».

razdacha распространяется под GPL-2.0-or-later — той же лицензией, что и Podkop.

**Товарный знак.** «Podkop» — товарный знак itdoginfo. Согласно
[trademark-политике Podkop](https://github.com/itdoginfo/podkop/blob/main/TRADEMARK.md)
razdacha не использует это имя, его вариации или логотип, и не является официальным
продуктом Podkop или производным от него по названию. Указание происхождения носит
исключительно описательный характер.

## DB-IP Country Lite

Определение страны сервера по IP работает офлайн, по встроенной в бинарник базе
[DB-IP Country Lite](https://db-ip.com/db/download/ip-to-country-lite)
(© [DB-IP](https://db-ip.com)). База распространяется под лицензией
[CC-BY-4.0](https://creativecommons.org/licenses/by/4.0/) и требует атрибуции:

> IP Geolocation by DB-IP (https://db-ip.com)

Атрибуция также вшита в бинарник (`internal/geoip`, константа `Attribution`) и
печатается в лог при первой загрузке базы. Файл базы — `internal/geoip/dbip-country-lite.mmdb`,
уезжает в бинарник через `go:embed` ([ADR 0017](docs/decisions/0017-country-default-tunnels.md)).
Ридер — чистый Go [oschwald/maxminddb-golang](https://github.com/oschwald/maxminddb-golang)
(© Gregory Oschwald, ISC), совместимый с `CGO_ENABLED=0`.

## Прочее

- [sing-box](https://github.com/SagerNet/sing-box) (© SagerNet, GPL-3.0-or-later) —
  движок маршрутизации. Запускается отдельным процессом, и при этом пакет
  `sing-box/option` линкуется в `razdachad` для типизированной сборки конфига
  ([ADR 0006](docs/decisions/0006-go-daemon.md)). razdacha распространяется под
  GPL-2.0-**or-later**, поэтому комбинация с GPL-3.0-кодом распространяется как
  GPL-3.0-or-later.
- [itdoginfo/allow-domains](https://github.com/itdoginfo/allow-domains) — курируемые
  списки домены/подсети сервисов, загружаются в рантайме.
- [wg-easy](https://github.com/wg-easy/wg-easy) — референс UX управления пирами
  (код не используется).
