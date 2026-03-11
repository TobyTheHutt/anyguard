#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
export PATH="$(go env GOPATH)/bin:${PATH}"

cd "${repo_root}/testdata/golangci"
golangci-lint custom
