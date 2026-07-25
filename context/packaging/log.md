# Packaging — log

<!-- Dated entries appended by the scribe agent, newest first. -->
<!-- Schema: `## YYYY-MM-DD` then `### <ref> — <title>` with Changed / New surface / Beware. -->

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
