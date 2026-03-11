#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
export PATH="$(go env GOPATH)/bin:${PATH}"

cd "${repo_root}/testdata/golangci"

custom_version="$(awk '/^version:/{print $2; exit}' .custom-gcl.yml | tr -d "\"'")"
if [[ -z "${custom_version}" ]]; then
	echo "unable to resolve golangci-lint version from .custom-gcl.yml" >&2
	exit 1
fi

local_mod_dir="$(go list -m -f '{{.Dir}}' "github.com/golangci/golangci-lint/v2@${custom_version}")"
if [[ -z "${local_mod_dir}" || ! -d "${local_mod_dir}" ]]; then
	echo "unable to locate golangci-lint module source for ${custom_version}" >&2
	exit 1
fi

tmp_root="$(mktemp -d)"
cleanup() {
	rm -rf "${tmp_root}"
}
trap cleanup EXIT

worktree="${tmp_root}/golangci-lint-src"
cp -R "${local_mod_dir}" "${worktree}"
chmod -R u+w "${worktree}"

git -C "${worktree}" init -q
git -C "${worktree}" config user.email "ci@example.invalid"
git -C "${worktree}" config user.name "CI"
git -C "${worktree}" add .
git -C "${worktree}" commit -q -m "local ${custom_version} source snapshot"
git -C "${worktree}" tag "${custom_version}"

local_repo="${tmp_root}/golangci-lint.git"
git clone --bare -q "${worktree}" "${local_repo}"

# Isolate git global config to this script and force the custom clone URL to local file://.
export GIT_CONFIG_GLOBAL="${tmp_root}/gitconfig"
touch "${GIT_CONFIG_GLOBAL}"
git config --global http.version HTTP/1.1
git config --global url."file://${local_repo}".insteadOf "https://github.com/golangci/golangci-lint.git"

max_attempts=3
attempt=1

while :; do
	if golangci-lint custom --verbose; then
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
