# proton - Development Makefile

PREFIX          ?= /usr/local
BINDIR          ?= $(PREFIX)/bin
SBINDIR         ?= $(PREFIX)/sbin
UNITDIR_USER    ?= $(PREFIX)/lib/systemd/user

.PHONY: test coverage coverage-html coverage-func lint fmt vet mod-tidy mod-verify \
        build clean help \
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
	@echo "                      and systemd user units to UNITDIR_USER ($(UNITDIR_USER))"

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

build: proton proton-fuse proton-redirector

proton: $(wildcard cmd/proton/*.go api/*.go api/**/*.go internal/**/*.go)
	go build -v -o proton ./cmd/proton/

proton-fuse: $(wildcard cmd/proton-fuse/*.go internal/**/*.go)
	GOOS=linux go build -v -o proton-fuse ./cmd/proton-fuse/

proton-redirector: $(wildcard cmd/proton-redirector/*.go internal/**/*.go)
	GOOS=linux go build -v -o proton-redirector ./cmd/proton-redirector/

clean:
	rm -f proton proton-fuse proton-redirector coverage.out coverage.html

install: install-proton-cli install-protonfs

install-proton-cli:
	@test -f proton || { echo "error: run 'make build' first"; exit 1; }
	install -d $(DESTDIR)$(BINDIR)
	install -m 0755 proton $(DESTDIR)$(BINDIR)/proton

install-protonfs:
	@test -f proton-fuse || { echo "error: run 'make build' first"; exit 1; }
	@test -f proton-redirector || { echo "error: run 'make build' first"; exit 1; }
	install -d $(DESTDIR)$(SBINDIR)
	install -m 0755 proton-fuse $(DESTDIR)$(SBINDIR)/proton-fuse
	install -m 4755 proton-redirector $(DESTDIR)$(SBINDIR)/proton-redirector
	install -d $(DESTDIR)$(BINDIR)
	install -m 0755 dist/protonctl $(DESTDIR)$(BINDIR)/protonctl
	install -d $(DESTDIR)$(UNITDIR_USER)
	sed 's|ExecStart=.*proton-redirector|ExecStart=$(SBINDIR)/proton-redirector|' \
		dist/protonfs-redirector.service > $(DESTDIR)$(UNITDIR_USER)/protonfs-redirector.service
	chmod 0644 $(DESTDIR)$(UNITDIR_USER)/protonfs-redirector.service
	sed 's|ExecStart=.*proton-fuse|ExecStart=$(SBINDIR)/proton-fuse|' \
		dist/protonfs.service > $(DESTDIR)$(UNITDIR_USER)/protonfs.service
	chmod 0644 $(DESTDIR)$(UNITDIR_USER)/protonfs.service
