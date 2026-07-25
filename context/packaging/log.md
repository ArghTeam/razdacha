# Packaging — log

<!-- Dated entries appended by the scribe agent, newest first. -->
<!-- Schema: `## YYYY-MM-DD` then `### <ref> — <title>` with Changed / New surface / Beware. -->

## 2026-07-25

### #2 — убран бинарник из репозитория

**Changed:** удалён `razdachad` (2 МБ, linux/arm64), приехавший вместе с настройкой CI; корневые пути сборки добавлены в `.gitignore`.
**Beware:** `.gitignore` закрывал только `/bin/`, а `go build` без `-o` кладёт бинарник в корень — так он и попал в коммит.

### #1 — CI на GitHub Actions

**Changed:** `.github/workflows/ci.yml` — три job: vet+test, кросс-сборка amd64/arm64, golangci-lint v2.12.2.
**New surface:** `cmd/razdachad` — точка входа демона, пока только флаг `--version`.
**Beware:** без единого Go-пакета `go vet ./...` и `go test ./...` возвращают 1 («matched no packages»). Триггеры — `pull_request` и `push` в `main`: на ветке задачи без PR проверки не идут.
