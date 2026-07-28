BIN     := razdachad
CLI     := razdacha
PKG     := ./cmd/$(BIN)
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
# Коммит подставляется линкером, а не берётся из вшитой информации о сборке:
# в связанном git-worktree `vcs.revision` показывает HEAD основного чекаута.
COMMIT  ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)

# статический бинарник — обязательное требование, см. docs/decisions/0006-go-daemon.md
export CGO_ENABLED := 0

.PHONY: all build build-cli test lint fmt ui clean deb check-go

all: build

check-go:
	@go version | grep -Eq 'go1\.(2[3-9]|[3-9][0-9])' || \
		{ echo "нужен Go >= 1.23 (зависимость sing-box/option), сейчас: $$(go version)"; exit 1; }

build: check-go
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BIN) $(PKG)

build-cli: check-go
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(CLI) ./cmd/$(CLI)

test:
	go test ./...

lint:
	golangci-lint run

fmt:
	gofumpt -l -w .

# Сборки у интерфейса нет: ui/dist — готовая статика без сборщика, она уезжает
# в бинарник через go:embed (см. tasks/22-ui.md). Цель оставлена, чтобы `make ui`
# не молчал и не создавал впечатление, что шаг сборки просто забыли.
ui:
	@test -f ui/dist/index.html || { echo "ui/dist/index.html не найден"; exit 1; }
	@echo "интерфейс собирать нечем: ui/dist встраивается в бинарник как есть"

# кросс-сборка под целевые архитектуры, см. docs/07-platform-support.md
.PHONY: build-linux
build-linux: check-go
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/linux-amd64/$(BIN) $(PKG)
	GOOS=linux GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/linux-arm64/$(BIN) $(PKG)

deb: build-linux
	nfpm package -f packaging/nfpm.yaml -p deb -t dist/

clean:
	rm -rf bin dist ui/build
