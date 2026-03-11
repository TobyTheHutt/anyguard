#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
export PATH="$(go env GOPATH)/bin:${PATH}"

# Harden git transport for GitHub runners.
# 1) Force HTTP/1.1 to avoid intermittent HTTP/2 transport issues.
# 2) If available, use GITHUB_TOKEN for authenticated GitHub clones.
git config --global http.version HTTP/1.1
if [[ -n "${GITHUB_TOKEN:-}" ]]; then
	git config --global url."https://x-access-token:${GITHUB_TOKEN}@github.com/".insteadOf "https://github.com/"
fi

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
