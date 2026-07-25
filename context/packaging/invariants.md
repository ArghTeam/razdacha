---
type: invariants
title: Packaging
description: systemd-юнит, nfpm, install/uninstall, матрица поддерживаемых ОС
---

## Invariants
- Версия Go задаётся один раз — в `go.mod`; CI читает её через `go-version-file`, числом нигде не дублируется.
- `CGO_ENABLED=0` проверяется отдельным шагом CI, а не подразумевается: нарушение роняет job (ADR 0006).
- Линт в CI и `make lint` используют один и тот же корневой `.golangci.yml`; конфиг не передаётся флагом.
- Целевые платформы закрываются кросс-сборкой `linux/amd64` и `linux/arm64`, а не матрицей ОС раннера.

## Key entry points
- `.github/workflows/ci.yml` — все автоматические проверки репозитория.
- `Makefile` — те же команды локально (`build`, `test`, `lint`, `build-linux`, `deb`).
- `cmd/razdachad/main.go` — точка входа демона.
