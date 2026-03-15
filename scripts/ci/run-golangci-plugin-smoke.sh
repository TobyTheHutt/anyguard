#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
smoke_dir="${repo_root}/testdata/golangci/smoke"
custom_binary="${repo_root}/testdata/golangci/custom-gcl"
build_script="${repo_root}/scripts/ci/build-custom-gcl.sh"
output_file="$(mktemp)"

trap 'rm -f "${output_file}"' EXIT

custom_binary_is_stale() {
	if [[ ! -x "${custom_binary}" ]]; then
		return 0
	fi

	find "${repo_root}" \
		\( -path "${repo_root}/.git" -o -path "${custom_binary}" -o -path "${repo_root}/testdata/golangci/custom-gcl.exe" \) -prune \
		-o -type f \
		\( -name '*.go' -o -name 'go.mod' -o -name 'go.sum' -o -name '*.yml' -o -name '*.yaml' -o -name '*.sh' \) \
		-newer "${custom_binary}" \
		-print -quit | grep -q .
}

if custom_binary_is_stale; then
	echo "custom golangci-lint binary is missing or stale. Rebuilding..."
	bash "${build_script}"
fi

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
