---
type: spec
tracker: "#1"
---
## TLDR

GitHub Actions на push и pull request: `go vet`, `go test`, кросс-сборка под
linux/amd64 и linux/arm64, `golangci-lint`. Версия Go — из `go.mod`.

## Goal

Каждое изменение проверяется автоматически, до появления кода — чтобы первые же
пакеты `internal/*` приезжали в репозиторий уже проверенными.

## Domains touched

- [packaging](../context/packaging/index.md) — сборка и целевые платформы

## Decisions

- 2026-07-25 — собираем на `ubuntu-latest` без матрицы ОС: целевые платформы
  закрываются кросс-сборкой `GOOS=linux GOARCH=amd64|arm64`, а матрица ОС
  проверяла бы окружение раннера, а не наш бинарник.
- 2026-07-25 — версия Go задаётся через `go-version-file: go.mod`, чтобы
  требование «Go ≥ 1.23» жило в одном месте.

## Acceptance criteria

- [ ] workflow зелёный на текущем состоянии репозитория
- [ ] сборка идёт с `CGO_ENABLED=0`, нарушение роняет job
- [ ] версия Go читается из `go.mod`, а не задана числом
- [ ] линт запускается тем же `.golangci.yml`, что и локальный `make lint`
