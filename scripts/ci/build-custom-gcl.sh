#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
export PATH="$(go env GOPATH)/bin:${PATH}"

source_custom_cfg="${repo_root}/testdata/golangci/.custom-gcl.yml"
custom_output_dir="${repo_root}/testdata/golangci"
fingerprint_script="${repo_root}/scripts/ci/custom-gcl-fingerprint.sh"

custom_version="$(awk '/^version:/{print $2; exit}' "${source_custom_cfg}" | tr -d "\"'")"
if [[ -z "${custom_version}" ]]; then
	echo "unable to resolve golangci-lint version from .custom-gcl.yml" >&2
	exit 1
fi

custom_name="$(awk '/^name:/{print $2; exit}' "${source_custom_cfg}" | tr -d "\"'")"
if [[ -z "${custom_name}" ]]; then
	echo "unable to resolve custom binary name from .custom-gcl.yml" >&2
	exit 1
fi

custom_fingerprint_file="${custom_output_dir}/${custom_name}.fingerprint"
custom_build_fingerprint="$(bash "${fingerprint_script}" "${custom_name}")"

plugin_module="$(awk '/^plugins:/ {in_plugins=1; next} in_plugins && /module:/ {for (i = 1; i <= NF; i++) if ($i == "module:") {print $(i + 1); exit}}' "${source_custom_cfg}" | tr -d "\"'")"
if [[ -z "${plugin_module}" ]]; then
	echo "unable to resolve plugin module from .custom-gcl.yml" >&2
	exit 1
fi
plugin_repo_module="${plugin_module}"
if [[ "${plugin_repo_module}" =~ ^(.+)/v[0-9]+$ ]]; then
	plugin_repo_module="${BASH_REMATCH[1]}"
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

export GODEBUG="${GODEBUG:+${GODEBUG},}http2client=0"

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
mkdir -p "${plugin_worktree}"
tar \
	--exclude='.git' \
	--exclude='testdata/golangci/custom-gcl' \
	--exclude='testdata/golangci/custom-gcl.exe' \
	-C "${repo_root}" \
	-cf - . | tar -C "${plugin_worktree}" -xf -
chmod -R u+w "${plugin_worktree}"

git -C "${plugin_worktree}" init -q
git -C "${plugin_worktree}" config user.email "ci@example.invalid"
git -C "${plugin_worktree}" config user.name "CI"
git -C "${plugin_worktree}" add .
git -C "${plugin_worktree}" commit -q -m "local source snapshot"

plugin_snapshot_version="$(git -C "${plugin_worktree}" rev-parse HEAD)"

custom_cfg_runtime="${tmp_root}/.custom-gcl.yml"
awk -v plugin_snapshot_version="${plugin_snapshot_version}" '
	/^plugins:/ { in_plugins=1 }
	in_plugins && /^[^[:space:]]/ && $0 !~ /^plugins:/ { in_plugins=0 }
	in_plugins && /^[[:space:]]+version:/ {
		sub(/version:[[:space:]].*/, "version: " plugin_snapshot_version)
	}
	{ print }
' "${source_custom_cfg}" > "${custom_cfg_runtime}"

plugin_repo="${tmp_root}/anyguard.git"
git clone --bare -q "${plugin_worktree}" "${plugin_repo}"

# Isolate git global config to this script and force the custom clone URL to local file://.
export GIT_CONFIG_GLOBAL="${tmp_root}/gitconfig"
touch "${GIT_CONFIG_GLOBAL}"
git config --global url."file://${local_repo}".insteadOf "https://github.com/golangci/golangci-lint.git"
git config --global url."file://${plugin_repo}".insteadOf "https://${plugin_repo_module}.git"
git config --global url."file://${plugin_repo}".insteadOf "https://${plugin_repo_module}"

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

append_csv_env GOPRIVATE "${plugin_repo_module}"
append_csv_env GOPRIVATE "${plugin_module}"
export GOPROXY="https://proxy.golang.org,direct"

max_attempts=3
attempt=1

cd "${tmp_root}"

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

mv -f "${tmp_root}/${custom_name}" "${custom_output_dir}/${custom_name}"
printf '%s\n' "${custom_build_fingerprint}" > "${custom_fingerprint_file}"
