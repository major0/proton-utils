# proton - Development Makefile

.PHONY: test coverage coverage-html coverage-func lint fmt vet mod-tidy mod-verify build proton-fuse proton-redirector clean help

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
	rm -f proton proton-fuse proton-redirector proton-cli coverage.out coverage.html
