#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
export PATH="$(go env GOPATH)/bin:${PATH}"

cd "${repo_root}/testdata/golangci"

max_attempts=3
attempt=1

while :; do
	if golangci-lint custom; then
		break
	fi

	if [[ "${attempt}" -ge "${max_attempts}" ]]; then
		echo "golangci-lint custom failed after ${max_attempts} attempts" >&2
		exit 1
	fi

	echo "golangci-lint custom failed (attempt ${attempt}/${max_attempts}); retrying..." >&2
	attempt=$((attempt + 1))
	sleep 5
done
