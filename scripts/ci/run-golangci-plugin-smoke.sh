#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
smoke_dir="${repo_root}/testdata/golangci/smoke"
custom_binary="${repo_root}/testdata/golangci/custom-gcl"
custom_fingerprint_file="${custom_binary}.fingerprint"
build_script="${repo_root}/scripts/ci/build-custom-gcl.sh"
fingerprint_script="${repo_root}/scripts/ci/custom-gcl-fingerprint.sh"
output_file="$(mktemp)"
cache_dir="$(mktemp -d)"

trap 'rm -f "${output_file}"; rm -rf "${cache_dir}"' EXIT

custom_binary_is_stale() {
	local current_fingerprint
	local recorded_fingerprint

	if [[ ! -x "${custom_binary}" || ! -f "${custom_fingerprint_file}" ]]; then
		return 0
	fi

	current_fingerprint="$(bash "${fingerprint_script}" "$(basename "${custom_binary}")")"
	recorded_fingerprint="$(tr -d '[:space:]' < "${custom_fingerprint_file}")"
	[[ -z "${recorded_fingerprint}" || "${current_fingerprint}" != "${recorded_fingerprint}" ]]
}

if custom_binary_is_stale; then
	echo "custom golangci-lint binary is missing or stale. Rebuilding..."
	bash "${build_script}"
fi

cd "${smoke_dir}"
export GOLANGCI_LINT_CACHE="${cache_dir}"

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
