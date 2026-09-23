#!/bin/sh
set -eu
POSIXLY_CORRECT='no bashing shell'

##
# ${*} = message to print to stderr
# returns: 0
error() {
  : "error(msg='${*}')"
  echo "error: ${*}" >&2
}

##
# ${*} = fatal message
# returns: does not return (exits 1)
die() {
  : "die(msg='${*}')"
  error "${*}"
  exit 1
}

# Installs Go development tools (goimports, golangci-lint).
# Appends GOPATH/bin to GITHUB_PATH if running in CI.

# Tool versions are pinned. Floating on latest means a new upstream release
# breaks CI on pull requests that changed nothing related.
GOIMPORTS_VERSION='v0.50.0'
GOLANGCI_LINT_VERSION='v2.13.2'

go install "golang.org/x/tools/cmd/goimports@${GOIMPORTS_VERSION}" || die 'failed to install goimports'

GOBIN="$(go env GOPATH)/bin"
# Installed through the module proxy rather than upstream's install.sh. That
# script matches release asset names without anchoring, so for any release that
# also ships an SBOM it reads the checksum of
# golangci-lint-<ver>-<os>-<arch>.tar.gz.sbom.json instead of the tarball and
# fails verification.
go install "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_LINT_VERSION}" || die 'failed to install golangci-lint'

if test -n "${GITHUB_PATH:-}"; then
	echo "${GOBIN}" >> "${GITHUB_PATH}"
fi

unset GOBIN GOIMPORTS_VERSION GOLANGCI_LINT_VERSION
