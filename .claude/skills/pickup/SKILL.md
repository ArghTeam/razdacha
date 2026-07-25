---
name: pickup
description: >-
  Взять GitHub Issue в работу: прочитать его, создать ветку, завести спеку
  tasks/<номер>-<slug>.md и пометить issue как in-progress. Проектная обёртка
  над ledger:pickup с поддержкой GitHub Issues вместо Linear. Использовать,
  когда пользователь говорит «возьми в работу #12», «начинаем задачу»,
  «пикапни issue», «/pickup», или даёт ссылку на issue со словами «делаем это».
---

# pickup (GitHub)

Обёртка над плагинным `ledger:pickup`. Поведение оригинала сохраняется целиком;
заменяются только шаги, которые ходят в трекер — вместо Linear используется
`gh` и GitHub Issues.

## Как читать оригинал

Найди оригинальный SKILL.md динамически, версию не хардкодь:

```bash
ls -d ~/.claude/plugins/cache/ledgerloop/ledger/*/skills/pickup/SKILL.md | sort -V | tail -1
```

Если команда ничего не вернула — скажи пользователю, что плагин `ledger` не
установлен, и остановись. Иначе прочитай найденный файл и следуй ему дословно:
правила slug, вывод handle для ветки, шаблон спеки, бюджет
`budgets.specLines`, подбор «Domains touched», форма отчёта. Ниже — только
замены.

## Проверка окружения

Перед первым сетевым вызовом выполни `gh auth status`. Если gh не авторизован —
скажи об этом прямо (`gh auth login`) и остановись, ничего не меняя.

Репозиторий вычисляй при каждом запуске из `ledgerloop.config.json`:
`<tracker.team>/<tracker.project>` (сейчас `ArghTeam/razdacha`). Не хардкодь.
Все вызовы `gh` — с явным `-R <repo>`.

## Вход

`$ARGUMENTS` — `12`, `#12` или полный URL
`https://github.com/<owner>/<repo>/issues/12`. Во всех случаях извлекается
голый номер `12`. Если аргумент пустой — спроси у пользователя номер issue и
ничего не делай до ответа.

## Замена шага 1 — чтение issue

```bash
gh issue view <N> -R <repo> --json number,title,body,state,labels,assignees,url
```

Из ответа берутся `title` (для slug и отчёта), `body` (для «Domains touched» и
acceptance criteria), `state`, `labels`, `assignees`.

Если `state` — `CLOSED`, скажи об этом и спроси, точно ли берём закрытый issue
в работу. До ответа не создавай ветку и не меняй метки.

## Замена шага 2/3 — идентификатор и ветка

`<id>` = `#<N>`, `<id-lower>` = голый номер `<N>`. Ветка — как в оригинале:
`<handle>/<id-lower>-<slug>`, например `roman/12-singbox-generator`.

Если ветка локально уже существует — `git checkout <branch>` вместо
`git checkout -b <branch>`, и отметь это в отчёте. Существующую ветку никогда
не пересоздавай и не удаляй.

## Замена шага 4 — статус In Progress

У GitHub Issues нет статусов: «In Progress» здесь выражается меткой
`in-progress`. Если метки нет в репозитории — создай, идемпотентно:

```bash
gh label create "in-progress" -R <repo> --description "взято в работу" --force
```

`--force` обновляет существующую метку вместо ошибки. Не глуши вывод через
`|| true` — так проглатывается и отказ по правам.

Затем:

```bash
gh issue edit <N> -R <repo> --add-label in-progress
```

Если в `assignees` пусто (или тебя там нет) — добавь `--add-assignee @me` тем
же вызовом. Если метка и назначение уже стоят — пропусти молча.

## Замена шага 5 — спека

Путь: `tasks/<N>-<slug>.md`. Если файл `tasks/<N>-*.md` уже есть — не создавай,
отметь в отчёте.

Фронтматтер, номер **обязательно в кавычках**:

```
---
type: spec
tracker: "#12"
---
```

Без кавычек YAML прочитает `#` как начало комментария и поле окажется пустым.

**Acceptance criteria** бери из тела issue, если они там есть — issue заводится
нашим `/capture`, который кладёт их в описание. Плейсхолдер оставляй только
когда в теле их нет.

**Domains touched** — сопоставь текст issue (title + body) с `layers[].description`
из `ledgerloop.config.json`, слои не выдумывай. Те же слои проставь метками на
issue, создавая недостающие:

```bash
gh label create "layer:<name>" -R <repo> --description "<описание слоя из конфига>" --force
gh issue edit <N> -R <repo> --add-label "layer:<name>" [--add-label "layer:<name2>" ...]
```

## Отчёт

Ровно в форме оригинала, по строке на пункт, плюс строка меток:

```
Issue:   #<N> — <title>
Branch:  <branch-name>  [created | already existed]
Spec:    tasks/<N>-<slug>.md  [created | already existed]
Domains touched: <список ссылок, или «none matched — заполнить на /ledger:preflight»>
Labels:  in-progress, layer:<...>
```

## Важно

- Скилл только читает issue и меняет метки/assignee — он никогда не создаёт и
  не закрывает issue (создаёт `/capture`, закрывает `/close`).
- Не делает `git commit`, `git push` и деструктивных git-команд
  (`reset --hard`, `checkout --`, `clean -f`).
- Список слоёв — только `layers[].name` из `ledgerloop.config.json`, читается
  при каждом запуске.
- Репозиторий не хардкодится: `<tracker.team>/<tracker.project>` из конфига.
