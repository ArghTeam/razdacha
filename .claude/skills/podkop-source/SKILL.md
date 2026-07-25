---
name: podkop-source
description: Найти, как что-то реализовано в Podkop (~/Web/podkop) — источнике архитектуры razdacha — и вернуть проверенные ссылки file:line. Использовать, когда нужно понять «как это сделано в подкопе», портировать логику (nft-правила, генерация конфига sing-box, разбор proxy-URL, работа со списками, диагностика), или сверить наше поведение с оригиналом.
---

# Поиск в исходниках Podkop

`~/Web/podkop` — репозиторий Podkop под OpenWrt, источник архитектуры razdacha.
**Только для чтения.** Никогда не редактируй и не коммить туда.

## Сначала — готовый разбор

`docs/research/podkop-analysis.md` уже содержит разбор с картой файлов, жизненным
циклом, таблицами «что переносится / что не переносится» и списком найденных дефектов.
**Прочитай его первым.** Скорее всего ответ там уже есть, и лезть в исходники не нужно.

Если разбора недостаточно — ищи в исходниках, а потом **допиши найденное в
`podkop-analysis.md`**, чтобы следующий раз обошёлся без поиска.

## Карта репозитория

```
podkop/files/usr/bin/podkop                      2770  CLI и вся бизнес-логика
podkop/files/usr/lib/sing_box_config_manager.sh  1508  конструктор JSON через jq
podkop/files/usr/lib/sing_box_config_facade.sh    335  разбор proxy-URL
podkop/files/usr/lib/helpers.sh                   355  валидаторы, URL-парсер, миграции
podkop/files/usr/lib/rulesets.sh                  180  rule-set sing-box
podkop/files/usr/lib/nft.sh                        69  обёртки над nft
podkop/files/usr/lib/constants.sh                  66  теги, URL, 24 сервиса
podkop/files/etc/config/podkop                     39  UCI-схема
podkop/files/etc/init.d/podkop                     52  procd-сервис
fe-app-podkop/src/                                      TypeScript UI (валидаторы, хелперы)
luci-app-podkop/htdocs/.../main.js                4942  СБОРКА, не исходник
String-example.md                                   118  примеры proxy-URL — тестовый корпус
```

Три уровня генерации конфига: `sing_box_configure_*` (в `bin/podkop`, читают UCI) →
`sing_box_cf_*` (facade, парсинг URL) → `sing_box_cm_*` (manager, jq-трансформации).

**`luci-app-podkop/htdocs/.../main.js` — автогенерированный файл**, собирается tsup из
`fe-app-podkop/src/`. Искать в нём бессмысленно, читай TS-исходники.

## Как искать

Функции определяются как `имя() {` в начале строки:

```
grep -n "^имя_функции" ~/Web/podkop/podkop/files/usr/bin/podkop
grep -rn "^sing_box_cm_" ~/Web/podkop/podkop/files/usr/lib/
```

Опции UCI:
```
grep -rn "config_get.*имя_опции" ~/Web/podkop/podkop/files/usr/bin/podkop
```

## Обязательная проверка ссылок

**Любую ссылку `file:line` подтверждай грепом перед тем, как записать её в документ
или ответ.** Строки уже разъезжались один раз: в первом разборе было заявлено 23 сервиса
в `COMMUNITY_SERVICES` вместо фактических 24.

То же касается любых чисел — размеров файлов, количества строк, длины списков.
Посчитай (`wc -l`, `grep -c`), не оценивай на глаз.

## Что докладывать

- Точные ссылки `file:line` на найденное.
- Что функция делает — своими словами, не переклеенным кодом. Фрагменты кода — не
  длиннее 5 строк.
- **Повторяем мы это или нет и почему.** Сверься с `docs/decisions/` и с таблицей
  «что не переносится» в `podkop-analysis.md`. Механика перехвата и FakeIP переносятся
  почти дословно; UCI, procd, LuCI, перехват dnsmasq, kernel-интерфейсы для исходящих
  туннелей и вся jq-архитектура — нет.
- Замеченные дефекты и странности — в раздел «Замечания» файла `podkop-analysis.md`.
  Не для критики проекта, а чтобы не воспроизвести те же места.
