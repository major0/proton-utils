# proton - Development Makefile

PREFIX  ?= /usr/local
BINDIR  ?= $(PREFIX)/bin
SBINDIR ?= $(PREFIX)/sbin

.PHONY: test coverage coverage-html coverage-func lint fmt vet mod-tidy mod-verify \
        build proton-fuse proton-redirector clean help \
        install install-proton-cli install-protonfs

help:
	@echo "proton Development Targets"
	@echo "=========================="
	@echo ""
	@echo "  test              - Run all tests"
	@echo "  coverage          - Generate coverage profile"
	@echo "  coverage-html     - Generate HTML coverage report"
	@echo "  coverage-func     - Display function-level coverage"
	@echo "  lint              - Run golangci-lint"
	@echo "  fmt               - Format Go code"
	@echo "  vet               - Run go vet"
	@echo "  mod-tidy          - Tidy go.mod and go.sum"
	@echo "  mod-verify        - Verify go.mod dependencies"
	@echo "  build             - Build proton binary"
	@echo "  proton-fuse       - Build proton-fuse binary (linux only)"
	@echo "  proton-redirector - Build proton-redirector binary (linux only)"
	@echo "  clean             - Remove generated files"
	@echo ""
	@echo "Install Targets (PREFIX=$(PREFIX))"
	@echo "=================================="
	@echo ""
	@echo "  install           - Install all binaries"
	@echo "  install-proton-cli - Install proton CLI to BINDIR ($(BINDIR))"
	@echo "  install-protonfs  - Install proton-fuse and proton-redirector to SBINDIR ($(SBINDIR))"

test:
	go test -v -race ./...

coverage:
	go test -coverprofile=coverage.out -covermode=atomic ./...

coverage-html: coverage
	go tool cover -html=coverage.out -o coverage.html

coverage-func: coverage
	go tool cover -func=coverage.out

lint:
	golangci-lint run --config .golangci.yml ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

mod-tidy:
	go mod tidy

mod-verify:
	go mod verify

build:
	go build -v -o proton ./cmd/proton/

proton-fuse:
	GOOS=linux go build -v -o proton-fuse ./cmd/proton-fuse/

proton-redirector:
	GOOS=linux go build -v -o proton-redirector ./cmd/proton-redirector/

clean:
	rm -f proton proton-fuse proton-redirector coverage.out coverage.html

install: install-proton-cli install-protonfs

install-proton-cli: build
	install -d $(DESTDIR)$(BINDIR)
	install -m 0755 proton $(DESTDIR)$(BINDIR)/proton

install-protonfs: proton-fuse proton-redirector
	install -d $(DESTDIR)$(SBINDIR)
	install -m 0755 proton-fuse $(DESTDIR)$(SBINDIR)/proton-fuse
	install -m 0755 proton-redirector $(DESTDIR)$(SBINDIR)/proton-redirector
