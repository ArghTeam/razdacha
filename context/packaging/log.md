# Packaging — log

<!-- Dated entries appended by the scribe agent, newest first. -->
<!-- Schema: `## YYYY-MM-DD` then `### <ref> — <title>` with Changed / New surface / Beware. -->

## 2026-07-28

### #33 — коммит сборки через линкер

**Changed:** `Makefile` и `release.yml` — `-X main.commit`; `buildInfo()` в `cmd/razdachad/main.go`.
**Beware:** коммит НЕ берётся из `debug.ReadBuildInfo`: в связанном git-worktree Go стемпит HEAD основного чекаута, а не ветки — проверено, `git describe` и `vcs.revision` разошлись. Реализация у нас всегда идёт в воркtree, встроенный источник врал бы каждый раз.

## 2026-07-27

### #90 — оповещения из окружения

**Changed:** `cmd/razdacha/notify.go` — засев оповещений при `setup`; `install.sh` и `docs/09-notifications.md`.
**New surface:** `RAZDACHA_TELEGRAM_TOKEN` и `RAZDACHA_TELEGRAM_CHAT`, три состояния как у `RAZDACHA_PUBLIC`.
**Beware:** токен читается прямо из окружения, а не флагом — аргументы процесса видны в `ps`. Переменные засевают БД, а не подменяют её.


## 2026-07-26

### #80, #81, #82 — путь обновления починен

**Changed:** `cmd/razdacha/*`, `install.sh`, `release.yml` — юниты получают `enable` + `restart`, режим панели переживает обновление, установщик ставит свою версию.
**New surface:** `systemctlRunner` (подменяемый `systemctl`), `packaging.PublicFromConfig`, `Installer.SavedPanelMode()`, `BUILT_TAG`, `install_sh_test.go`.
**Beware:** правила — в инвариантах слоя; дефект жил в релизе.

### стенд — обновление проверяется только обновлением

**Changed:** релизная проверка `v0.1.1 → v0.2.0` вскрыла два дефекта пути обновления, которых не видел ни один тест: демон не перезапускался, публичный режим терялся. Оба жили в выпущенном релизе.
**Beware:** до этого на стенд заливали бинарник через `install -m 0755`, мимо установщика — путь живого человека не прогонялся ни разу. Как проверять — в инвариантах слоя.

### #52 — install.sh, юниты и релизы

**Changed:** `packaging/install.sh` (фаза проверок в shell, изменения в Go через `razdacha setup`), юниты systemd, `release.yml` на пуш тега, `internal/qr`.
**New surface:** `razdacha setup`, `razdacha uninstall`, переменные `RAZDACHA_PUBLIC`, `RAZDACHA_DRY_RUN`, `RAZDACHA_VERSION`.
**Beware:** имена ассетов релиза и публичность репозитория — в инвариантах слоя.

### установка на чистой машине — три находки

**Changed:** nginx перезапускается вместо перечитывания; `-purge` снимает и `/etc/razdacha` с ключом панели.
**Beware:** на обжитой машине эти баги не воспроизводились — nginx там уже слушал наши адреса, а каталог с сертификатом существовал до установки.


## 2026-07-25

### #16 — проверка nginx на стенде

**Changed:** проверено на Debian 13 / nginx 1.26.3: `nginx -t`, TLS, WebSocket, отсутствие публичных сокетов, старт без `wg0` и переживание перезагрузки.
**Beware:** после снятия `sites-enabled/default` нужен `restart`, а не `reload`: старый воркер держит `0.0.0.0:80`, пока завершается.

### #10 — sing-box check в CI

**Changed:** job `singbox-check` — тег релиза выводится из `go.mod`, бинарник кэшируется, `sing-box check -c` гоняется по `internal/singbox/testdata/*.json`.
**Beware:** тег релиза и имя ассета отличаются формой (`v1.12.25` против `1.12.25`). Job сверяет `sing-box version` с версией из `go.mod` и падает при расхождении.


## 2026-07-25

### #2 — убран бинарник из репозитория

**Changed:** удалён `razdachad` (2 МБ, linux/arm64), приехавший вместе с настройкой CI; корневые пути сборки добавлены в `.gitignore`.
**Beware:** `.gitignore` закрывал только `/bin/`, а `go build` без `-o` кладёт бинарник в корень — так он и попал в коммит.

### #1 — CI на GitHub Actions

**Changed:** `.github/workflows/ci.yml` — три job: vet+test, кросс-сборка amd64/arm64, golangci-lint v2.12.2.
**New surface:** `cmd/razdachad` — точка входа демона, пока только флаг `--version`.
**Beware:** без единого Go-пакета `go vet ./...` и `go test ./...` возвращают 1 («matched no packages»). Триггеры — `pull_request` и `push` в `main`: на ветке задачи без PR проверки не идут.
