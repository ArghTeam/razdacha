BIN     := razdachad
CLI     := razdacha
PKG     := ./cmd/$(BIN)
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

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

ui:
	cd ui && npm ci && npm run build

# кросс-сборка под целевые архитектуры, см. docs/07-platform-support.md
.PHONY: build-linux
build-linux: check-go
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/linux-amd64/$(BIN) $(PKG)
	GOOS=linux GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/linux-arm64/$(BIN) $(PKG)

deb: build-linux
	nfpm package -f packaging/nfpm.yaml -p deb -t dist/

clean:
	rm -rf bin dist ui/build
