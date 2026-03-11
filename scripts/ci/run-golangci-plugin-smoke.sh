#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
smoke_dir="${repo_root}/testdata/golangci/smoke"
custom_binary="${repo_root}/testdata/golangci/custom-gcl"
output_file="$(mktemp)"

trap 'rm -f "${output_file}"' EXIT

cd "${smoke_dir}"

set +e
"${custom_binary}" run -c .golangci.yml ./... 2>&1 | tee "${output_file}"
status="${PIPESTATUS[0]}"
set -e

if [[ "${status}" -eq 0 ]]; then
	echo "expected non-zero exit code from smoke run"
	exit 1
fi

if ! grep -q "pkg/bad/bad.go" "${output_file}"; then
	echo "expected diagnostic for pkg/bad/bad.go"
	exit 1
fi

if grep -q "pkg/ok/ok.go" "${output_file}"; then
	echo "unexpected diagnostic for allowlisted file pkg/ok/ok.go"
	exit 1
fi

if grep -q "pkg/safe/safe.go" "${output_file}"; then
	echo "unexpected diagnostic for safe file pkg/safe/safe.go"
	exit 1
fi
