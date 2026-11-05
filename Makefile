# Tareas del proyecto. Nada de magia: los mismos comandos que se lanzan a mano.
BIN     := wibox-daemon
DIST    := dist
VERSION := $(shell sed -n 's/^version: "\(.*\)"/\1/p' wibox/config.yaml)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: check fmt vet test race cover build release

## check: lo que exige el CI, en el mismo orden
check: fmt vet test

## fmt: falla si hay ficheros sin gofmt
fmt:
	@test -z "$$(gofmt -l .)" || { echo "sin gofmt:"; gofmt -l .; exit 1; }

vet:
	go vet ./...

test:
	go test -count=1 ./...

race:
	go test -race -count=1 ./...

cover:
	go test -count=1 -coverprofile=cover.out ./... >/dev/null
	go tool cover -func=cover.out | tail -1

## build: binario local con la version del add-on
build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) .

## release: binarios de las dos arquitecturas con la version del add-on
release:
	@echo "version: $(VERSION)"
	mkdir -p $(DIST)
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/$(BIN)-linux-amd64 .
	GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/$(BIN)-linux-arm64 .
	@ls -l $(DIST) | tail -2
	@echo "ahora: git tag v$(VERSION) y gh release create v$(VERSION) $(DIST)/*"
