#!/usr/bin/env bash
set -euo pipefail

release_mode="${RELEASE_MODE:-publish}"
changelog_path="${1:-CHANGELOG.md}"
release_notes_root="${RUNNER_TEMP:-${TMPDIR:-/tmp}}/anyguard-release"
release_heading_pattern='^##[[:space:]]+\[v?([0-9]+\.[0-9]+\.[0-9]+)\][[:space:]]+-[[:space:]]+([0-9]{4}-[0-9]{2}-[0-9]{2})$'
release_subject_pattern='^release:[[:space:]]+(v[0-9]+\.[0-9]+\.[0-9]+)$'

top_release_heading_awk() {
	cat <<'AWK'
	$0 == "## [Unreleased]" {
		after_unreleased = 1
		next
	}
	after_unreleased && /^## / {
		print
		exit
	}
AWK
}

fail() {
	local message="$1"

	echo "release preparation failed: ${message}" >&2
	exit 1
}

set_action_output() {
	local name="$1"
	local value="$2"

	if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
		printf '%s=%s\n' "${name}" "${value}" >> "${GITHUB_OUTPUT}"
	fi

	printf '%s=%s\n' "${name}" "${value}"
}

read_current_commit_subject() {
	git log -1 --pretty='%s'
}

read_release_signal_title() {
	if [[ "${release_mode}" == "validate-pr" && -n "${RELEASE_PR_TITLE:-}" ]]; then
		printf '%s\n' "${RELEASE_PR_TITLE}"
		return
	fi

	read_current_commit_subject
}

read_top_release_heading_from_file() {
	local file_path="$1"

	awk "$(top_release_heading_awk)" "${file_path}"
}

read_previous_top_release_heading() {
	local file_path="$1"
	local parent_changelog_ref="HEAD^:${file_path}"

	if ! git cat-file -e "${parent_changelog_ref}" 2>/dev/null; then
		return 0
	fi

	git show "${parent_changelog_ref}" | awk "$(top_release_heading_awk)"
}

read_release_body() {
	local file_path="$1"

	awk '
		$0 == "## [Unreleased]" {
			after_unreleased = 1
			next
		}
		after_unreleased && /^## / && ! in_release {
			in_release = 1
			next
		}
		in_release && /^## / {
			exit
		}
		in_release {
			lines[++line_count] = $0
		}
		END {
			first_line = 1
			last_line = line_count

			while (first_line <= last_line && lines[first_line] == "") {
				first_line++
			}
			while (last_line >= first_line && lines[last_line] == "") {
				last_line--
			}
			for (line = first_line; line <= last_line; line++) {
				print lines[line]
			}
		}
	' "${file_path}"
}

read_remote_tag_status() {
	local release_tag="$1"
	local tag_ref="refs/tags/${release_tag}"
	local status

	if ! git remote get-url origin >/dev/null 2>&1; then
		printf 'missing\n'
		return
	fi

	if git ls-remote --exit-code --tags origin "${tag_ref}" >/dev/null 2>&1; then
		printf 'exists\n'
		return
	fi

	status="$?"
	if [[ "${status}" == "2" ]]; then
		printf 'missing\n'
		return
	fi

	printf 'error\n'
}

changelog_changed_in_head() {
	local file_path="$1"

	if ! git rev-parse --verify --quiet HEAD^ >/dev/null; then
		printf 'false\n'
		return
	fi

	if git diff --quiet HEAD^ HEAD -- "${file_path}"; then
		printf 'false\n'
		return
	fi

	printf 'true\n'
}

release_label_is_set() {
	local labels="${RELEASE_PR_LABELS:-}"
	local label
	local trimmed_label

	if [[ "${release_mode}" != "validate-pr" ]]; then
		printf 'false\n'
		return
	fi

	IFS=',' read -r -a label_names <<< "${labels}"
	for label in "${label_names[@]}"; do
		trimmed_label="$(printf '%s' "${label}" | xargs)"

		if [[ "${trimmed_label}" == "release" ]]; then
			printf 'true\n'
			return
		fi
	done

	printf 'false\n'
}

extract_release_tag_from_subject() {
	local commit_subject="$1"

	if [[ "${commit_subject}" =~ ${release_subject_pattern} ]]; then
		printf '%s\n' "${BASH_REMATCH[1]}"
		return
	fi

	if [[ "${commit_subject}" == release:* ]]; then
		fail "release commit subject must match 'release: vX.Y.Z', got '${commit_subject}'"
	fi
}

parse_release_heading() {
	local release_heading="$1"

	if [[ ! "${release_heading}" =~ ${release_heading_pattern} ]]; then
		return 1
	fi

	printf 'v%s|%s\n' "${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}"
}

tag_exists_locally() {
	local release_tag="$1"
	local tag_ref="refs/tags/${release_tag}"

	git show-ref --tags --verify --quiet "${tag_ref}"
}

fetch_remote_tag() {
	local release_tag="$1"
	local tag_ref="refs/tags/${release_tag}"

	git fetch --force --no-tags origin "${tag_ref}:${tag_ref}" >/dev/null
}

read_tag_commit() {
	local release_tag="$1"
	local tag_ref="refs/tags/${release_tag}"

	git rev-parse "${tag_ref}^{commit}"
}

require_tag_available_for_pr() {
	local release_tag="$1"
	local local_tag_exists="false"
	local remote_tag_status

	remote_tag_status="$(read_remote_tag_status "${release_tag}")"
	if [[ "${remote_tag_status}" == "error" ]]; then
		fail "unable to verify whether tag ${release_tag} exists on origin"
	fi
	if tag_exists_locally "${release_tag}"; then
		local_tag_exists="true"
	fi
	if [[ "${remote_tag_status}" == "exists" || "${local_tag_exists}" == "true" ]]; then
		fail "tag ${release_tag} already exists; choose an unreleased version"
	fi
}

require_existing_tag_matches_head() {
	local release_tag="$1"
	local existing_tag_commit
	local head_commit
	local remote_tag_status

	remote_tag_status="$(read_remote_tag_status "${release_tag}")"
	if [[ "${remote_tag_status}" == "error" ]]; then
		fail "unable to verify whether tag ${release_tag} exists on origin"
	fi
	if [[ "${remote_tag_status}" == "exists" ]]; then
		if ! fetch_remote_tag "${release_tag}"; then
			fail "unable to fetch existing remote tag ${release_tag}"
		fi
	fi

	if ! tag_exists_locally "${release_tag}"; then
		printf 'false\n'
		return
	fi

	# Re-runs are allowed only when the existing tag already peels to HEAD.
	head_commit="$(git rev-parse HEAD)"
	existing_tag_commit="$(read_tag_commit "${release_tag}")"
	if [[ "${existing_tag_commit}" != "${head_commit}" ]]; then
		fail "tag ${release_tag} points to ${existing_tag_commit}, expected ${head_commit}"
	fi

	printf 'true\n'
}

write_skip_outputs() {
	set_action_output "release_detected" "false"
	set_action_output "tag" ""
	set_action_output "date" ""
	set_action_output "body_file" ""
	set_action_output "tag_exists" "false"
}

mkdir -p "${release_notes_root}"

if [[ "${release_mode}" != "publish" && "${release_mode}" != "validate-pr" ]]; then
	fail "unsupported release mode ${release_mode}"
fi

if [[ ! -f "${changelog_path}" ]]; then
	fail "${changelog_path} does not exist"
fi

release_signal_title="$(read_release_signal_title)"
subject_release_tag="$(extract_release_tag_from_subject "${release_signal_title}")"
current_release_heading="$(read_top_release_heading_from_file "${changelog_path}")"
previous_release_heading="$(read_previous_top_release_heading "${changelog_path}")"
changelog_changed="$(changelog_changed_in_head "${changelog_path}")"
changelog_heading_changed="false"
release_label_detected="$(release_label_is_set)"

if [[ "${changelog_changed}" == "true" && "${current_release_heading}" != "${previous_release_heading}" ]]; then
	changelog_heading_changed="true"
fi

if [[ -z "${subject_release_tag}" && "${release_label_detected}" == "false" && "${changelog_heading_changed}" == "false" ]]; then
	write_skip_outputs
	exit 0
fi

if [[ -z "${current_release_heading}" ]]; then
	fail "${changelog_path} does not contain a release section after Unreleased"
fi

if ! parsed_release_heading="$(parse_release_heading "${current_release_heading}")"; then
	fail "top changelog release heading is malformed: ${current_release_heading}"
fi

release_tag="${parsed_release_heading%%|*}"
release_date="${parsed_release_heading#*|}"

if [[ -n "${subject_release_tag}" && "${subject_release_tag}" != "${release_tag}" ]]; then
	fail "release subject tag ${subject_release_tag} does not match changelog tag ${release_tag}"
fi

release_body="$(read_release_body "${changelog_path}")"
release_body_file="${release_notes_root}/${release_tag}.md"

printf '%s\n' "${release_body}" > "${release_body_file}"

if ! grep -q '[^[:space:]]' "${release_body_file}"; then
	fail "top changelog release section ${release_tag} has an empty body"
fi

# PR validation must prove the version is unused before the post-merge tag exists.
if [[ "${release_mode}" == "validate-pr" ]]; then
	require_tag_available_for_pr "${release_tag}"
	set_action_output "release_detected" "true"
	set_action_output "tag" "${release_tag}"
	set_action_output "date" "${release_date}"
	set_action_output "body_file" "${release_body_file}"
	set_action_output "tag_exists" "false"
	exit 0
fi

# Publish mode permits reruns only when the existing tag already resolves to HEAD.
release_tag_exists="$(require_existing_tag_matches_head "${release_tag}")"

set_action_output "release_detected" "true"
set_action_output "tag" "${release_tag}"
set_action_output "date" "${release_date}"
set_action_output "body_file" "${release_body_file}"
set_action_output "tag_exists" "${release_tag_exists}"
