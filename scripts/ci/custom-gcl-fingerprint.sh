#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
custom_name="${1:-custom-gcl}"

(
	cd "${repo_root}"
	find . \
		\( -path './.git' \
			-o -path "./testdata/golangci/${custom_name}" \
			-o -path "./testdata/golangci/${custom_name}.exe" \
			-o -path "./testdata/golangci/${custom_name}.fingerprint" \) -prune \
		-o -type f \
		\( -name '*.go' -o -name 'go.mod' -o -name 'go.sum' -o -name '*.yml' -o -name '*.yaml' -o -name '*.sh' \) \
		-print0 \
		| LC_ALL=C sort -z \
		| xargs -0 sha256sum \
		| sha256sum \
		| awk '{print $1}'
)
