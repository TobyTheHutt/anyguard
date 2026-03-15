#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
export PATH="$(go env GOPATH)/bin:${PATH}"

custom_cfg="${repo_root}/testdata/golangci/.custom-gcl.yml"
cd "$(dirname "${custom_cfg}")"

custom_version="$(awk '/^version:/{print $2; exit}' .custom-gcl.yml | tr -d "\"'")"
if [[ -z "${custom_version}" ]]; then
	echo "unable to resolve golangci-lint version from .custom-gcl.yml" >&2
	exit 1
fi

plugin_module="$(awk '/^plugins:/ {in_plugins=1; next} in_plugins && /module:/ {for (i = 1; i <= NF; i++) if ($i == "module:") {print $(i + 1); exit}}' .custom-gcl.yml | tr -d "\"'")"
if [[ -z "${plugin_module}" ]]; then
	echo "unable to resolve plugin module from .custom-gcl.yml" >&2
	exit 1
fi

plugin_version="$(awk '/^plugins:/ {in_plugins=1; next} in_plugins && /version:/ {for (i = 1; i <= NF; i++) if ($i == "version:") {print $(i + 1); exit}}' .custom-gcl.yml | tr -d "\"'")"
if [[ -z "${plugin_version}" ]]; then
	echo "unable to resolve plugin version from .custom-gcl.yml" >&2
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

export GOMODCACHE="${tmp_root}/gomodcache"
export GOCACHE="${tmp_root}/gocache"

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

plugin_worktree="${tmp_root}/anyguard-src"
cp -R "${repo_root}" "${plugin_worktree}"
chmod -R u+w "${plugin_worktree}"
rm -rf "${plugin_worktree}/.git"
rm -f "${plugin_worktree}/testdata/golangci/custom-gcl" "${plugin_worktree}/testdata/golangci/custom-gcl.exe"

git -C "${plugin_worktree}" init -q
git -C "${plugin_worktree}" config user.email "ci@example.invalid"
git -C "${plugin_worktree}" config user.name "CI"
git -C "${plugin_worktree}" add .
git -C "${plugin_worktree}" commit -q -m "local ${plugin_version} source snapshot"
git -C "${plugin_worktree}" tag "${plugin_version}"

plugin_repo="${tmp_root}/anyguard.git"
git clone --bare -q "${plugin_worktree}" "${plugin_repo}"

# Isolate git global config to this script and force the custom clone URL to local file://.
export GIT_CONFIG_GLOBAL="${tmp_root}/gitconfig"
touch "${GIT_CONFIG_GLOBAL}"
git config --global http.version HTTP/1.1
git config --global url."file://${local_repo}".insteadOf "https://github.com/golangci/golangci-lint.git"
git config --global url."file://${plugin_repo}".insteadOf "https://${plugin_module}.git"
git config --global url."file://${plugin_repo}".insteadOf "https://${plugin_module}"

append_csv_env() {
	local key="$1"
	local value="$2"
	local current="${!key:-}"

	if [[ -z "${current}" ]]; then
		export "${key}=${value}"
		return
	fi
	if [[ ",${current}," == *",${value},"* ]]; then
		return
	fi
	export "${key}=${current},${value}"
}

append_csv_env GOPRIVATE "${plugin_module}"
append_csv_env GONOSUMDB "${plugin_module}"
export GOPROXY=direct

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

	echo "golangci-lint custom failed on attempt ${attempt}/${max_attempts}. Retrying..." >&2
	attempt=$((attempt + 1))
	sleep 5
done
