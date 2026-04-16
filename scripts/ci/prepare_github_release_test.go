package ci_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	changelogFile      = "CHANGELOG.md"
	gitAddCommand      = "add"
	gitCommand         = "git"
	gitConfigCommand   = "config"
	gitInitCommand     = "init"
	gitOriginRemote    = "origin"
	gitTagCommand      = "tag"
	githubOutputFile   = "github-output"
	modeValidatePR     = "validate-pr"
	notesCommitMessage = "docs: update notes"
	notesContent       = "ordinary documentation update\n"
	notesFile          = "NOTES.md"
	outputBodyFile     = "body_file"
	outputDate         = "date"
	outputFalse        = "false"
	outputRelease      = "release_detected"
	outputTag          = "tag"
	outputTagExists    = "tag_exists"
	outputTrue         = "true"
	releaseBody        = "### Added\n\n- Added automated release tagging."
	releaseDate        = "2026-04-14"
	releaseTag         = "v1.1.0"
	releaseVersion     = "1.1.0"
	releaseModeEnv     = "RELEASE_MODE"
	releaseNotesCommit = "docs: prepare release notes"
	releasePRTitleEnv  = "RELEASE_PR_TITLE"
	scriptTimeout      = 15 * time.Second
	seedCommitMessage  = "docs: seed changelog"
	tagExistsFragment  = "already exists"
	releaseCommitTitle = "release: v1.1.0"
)

type releaseRepo struct {
	dir        string
	scriptPath string
}

type prepareOutput struct {
	bodyFile        string
	date            string
	releaseDetected string
	tag             string
	tagExists       string
}

func TestPrepareGitHubReleaseDetectsReleaseSubject(t *testing.T) {
	repo := newReleaseRepo(t)

	repo.writeChangelog(t, changelogWithTopRelease(releaseVersion, releaseBody))
	repo.commit(t, releaseCommitTitle)

	output := repo.requirePrepareSuccess(t)

	requireOutputValue(t, output.releaseDetected, outputTrue, outputRelease)
	requireOutputValue(t, output.tag, releaseTag, outputTag)
	requireOutputValue(t, output.date, releaseDate, outputDate)
	requireOutputValue(t, output.tagExists, outputFalse, outputTagExists)
	requireReleaseBody(t, output.bodyFile, releaseBody)
}

func TestPrepareGitHubReleaseDetectsChangelogRelease(t *testing.T) {
	repo := newReleaseRepo(t)

	repo.writeChangelog(t, changelogWithTopRelease(releaseVersion, releaseBody))
	repo.commit(t, "docs: finalize release notes")

	output := repo.requirePrepareSuccess(t)

	requireOutputValue(t, output.releaseDetected, outputTrue, outputRelease)
	requireOutputValue(t, output.tag, releaseTag, outputTag)
	requireReleaseBody(t, output.bodyFile, releaseBody)
}

func TestPrepareGitHubReleaseSkipsNormalCommit(t *testing.T) {
	repo := newReleaseRepo(t)

	writeFile(t, repo.dir, notesFile, notesContent)
	repo.commit(t, notesCommitMessage)

	output := repo.requirePrepareSuccess(t)

	requireOutputValue(t, output.releaseDetected, outputFalse, outputRelease)
	requireOutputValue(t, output.tag, "", outputTag)
	requireOutputValue(t, output.tagExists, outputFalse, outputTagExists)
}

func TestPrepareGitHubReleaseValidatesReleasePRTitle(t *testing.T) {
	repo := newReleaseRepo(t)

	repo.writeChangelog(t, changelogWithTopRelease(releaseVersion, releaseBody))
	repo.commit(t, releaseNotesCommit)

	output := repo.requirePrepareSuccess(t, validatePREnvironment(releaseCommitTitle)...)

	requireOutputValue(t, output.releaseDetected, outputTrue, outputRelease)
	requireOutputValue(t, output.tag, releaseTag, outputTag)
	requireOutputValue(t, output.date, releaseDate, outputDate)
	requireOutputValue(t, output.tagExists, outputFalse, outputTagExists)
	requireReleaseBody(t, output.bodyFile, releaseBody)
}

func TestPrepareGitHubReleaseValidatesReleasePRWithOriginAndNoRemoteTag(t *testing.T) {
	repo := newReleaseRepo(t)

	repo.addOrigin(t)
	repo.writeChangelog(t, changelogWithTopRelease(releaseVersion, releaseBody))
	repo.commit(t, releaseNotesCommit)

	output := repo.requirePrepareSuccess(t, validatePREnvironment(releaseCommitTitle)...)

	requireOutputValue(t, output.releaseDetected, outputTrue, outputRelease)
	requireOutputValue(t, output.tag, releaseTag, outputTag)
	requireOutputValue(t, output.tagExists, outputFalse, outputTagExists)
	requireReleaseBody(t, output.bodyFile, releaseBody)
}

func TestPrepareGitHubReleaseValidatesReleasePRChangelog(t *testing.T) {
	repo := newReleaseRepo(t)

	repo.writeChangelog(t, changelogWithTopRelease(releaseVersion, releaseBody))
	repo.commit(t, releaseNotesCommit)

	output := repo.requirePrepareSuccess(t, envAssignment(releaseModeEnv, modeValidatePR))

	requireOutputValue(t, output.releaseDetected, outputTrue, outputRelease)
	requireOutputValue(t, output.tag, releaseTag, outputTag)
	requireReleaseBody(t, output.bodyFile, releaseBody)
}

func TestPrepareGitHubReleaseSkipsNormalPR(t *testing.T) {
	repo := newReleaseRepo(t)

	writeFile(t, repo.dir, notesFile, notesContent)
	repo.commit(t, notesCommitMessage)

	output := repo.requirePrepareSuccess(t, envAssignment(releaseModeEnv, modeValidatePR))

	requireOutputValue(t, output.releaseDetected, outputFalse, outputRelease)
	requireOutputValue(t, output.tag, "", outputTag)
	requireOutputValue(t, output.tagExists, outputFalse, outputTagExists)
}

func TestPrepareGitHubReleaseRejectsMalformedReleasePRTitle(t *testing.T) {
	repo := newReleaseRepo(t)

	combinedOutput := repo.requirePrepareFailure(t, validatePREnvironment("release: 1.1.0")...)

	if !strings.Contains(combinedOutput, "must match 'release: vX.Y.Z'") {
		t.Fatalf("expected malformed release PR title failure, got:\n%s", combinedOutput)
	}
}

func TestPrepareGitHubReleaseRejectsReleasePRLocalTagConflict(t *testing.T) {
	repo := newReleaseRepo(t)

	runCommand(t, repo.dir, gitCommand, gitTagCommand, releaseTag)
	repo.writeChangelog(t, changelogWithTopRelease(releaseVersion, releaseBody))
	repo.commit(t, releaseNotesCommit)

	combinedOutput := repo.requirePrepareFailure(t, validatePREnvironment(releaseCommitTitle)...)

	if !strings.Contains(combinedOutput, tagExistsFragment) {
		t.Fatalf("expected local tag conflict failure, got:\n%s", combinedOutput)
	}
}

func TestPrepareGitHubReleaseRejectsReleasePRRemoteTagConflict(t *testing.T) {
	repo := newReleaseRepo(t)

	repo.addOriginWithTag(t, releaseTag)
	repo.writeChangelog(t, changelogWithTopRelease(releaseVersion, releaseBody))
	repo.commit(t, releaseNotesCommit)

	combinedOutput := repo.requirePrepareFailure(t, validatePREnvironment(releaseCommitTitle)...)

	if !strings.Contains(combinedOutput, tagExistsFragment) {
		t.Fatalf("expected remote tag conflict failure, got:\n%s", combinedOutput)
	}
}

func TestPrepareGitHubReleaseRejectsSubjectChangelogMismatch(t *testing.T) {
	repo := newReleaseRepo(t)

	repo.writeChangelog(t, changelogWithTopRelease(releaseVersion, releaseBody))
	repo.commit(t, "release: v1.2.0")

	combinedOutput := repo.requirePrepareFailure(t)

	if !strings.Contains(combinedOutput, "does not match changelog tag") {
		t.Fatalf("expected mismatch failure, got:\n%s", combinedOutput)
	}
}

func TestPrepareGitHubReleaseRejectsMalformedTopHeading(t *testing.T) {
	repo := newReleaseRepo(t)

	malformedChangelog := changelogWithMalformedTopRelease(releaseBody)
	repo.writeChangelog(t, malformedChangelog)
	repo.commit(t, "docs: finalize malformed release notes")

	combinedOutput := repo.requirePrepareFailure(t)

	if !strings.Contains(combinedOutput, "top changelog release heading is malformed") {
		t.Fatalf("expected malformed heading failure, got:\n%s", combinedOutput)
	}
}

func TestPrepareGitHubReleaseRejectsEmptyReleaseBody(t *testing.T) {
	repo := newReleaseRepo(t)

	repo.writeChangelog(t, changelogWithTopRelease(releaseVersion, ""))
	repo.commit(t, releaseCommitTitle)

	combinedOutput := repo.requirePrepareFailure(t)

	if !strings.Contains(combinedOutput, "empty body") {
		t.Fatalf("expected empty release body failure, got:\n%s", combinedOutput)
	}
}

func TestPrepareGitHubReleaseRejectsMissingChangelog(t *testing.T) {
	repo := newReleaseRepo(t)

	removeFile(t, repo.dir, changelogFile)
	repo.commit(t, releaseCommitTitle)

	combinedOutput := repo.requirePrepareFailure(t)

	if !strings.Contains(combinedOutput, changelogFile+" does not exist") {
		t.Fatalf("expected missing changelog failure, got:\n%s", combinedOutput)
	}
}

func TestPrepareGitHubReleaseAcceptsExistingTagOnHead(t *testing.T) {
	repo := newReleaseRepo(t)

	repo.writeChangelog(t, changelogWithTopRelease(releaseVersion, releaseBody))
	repo.commit(t, releaseCommitTitle)
	runCommand(t, repo.dir, gitCommand, gitTagCommand, releaseTag)

	output := repo.requirePrepareSuccess(t)

	requireOutputValue(t, output.releaseDetected, outputTrue, outputRelease)
	requireOutputValue(t, output.tagExists, outputTrue, outputTagExists)
}

func TestPrepareGitHubReleaseAllowsConfiguredOriginWithoutRemoteTag(t *testing.T) {
	repo := newReleaseRepo(t)

	repo.addOrigin(t)
	repo.writeChangelog(t, changelogWithTopRelease(releaseVersion, releaseBody))
	repo.commit(t, releaseCommitTitle)

	output := repo.requirePrepareSuccess(t)

	requireOutputValue(t, output.releaseDetected, outputTrue, outputRelease)
	requireOutputValue(t, output.tag, releaseTag, outputTag)
	requireOutputValue(t, output.tagExists, outputFalse, outputTagExists)
	requireReleaseBody(t, output.bodyFile, releaseBody)
}

func TestPrepareGitHubReleaseRejectsExistingTagOffHead(t *testing.T) {
	repo := newReleaseRepo(t)

	runCommand(t, repo.dir, gitCommand, gitTagCommand, releaseTag)
	repo.writeChangelog(t, changelogWithTopRelease(releaseVersion, releaseBody))
	repo.commit(t, releaseCommitTitle)

	combinedOutput := repo.requirePrepareFailure(t)

	if !strings.Contains(combinedOutput, "tag "+releaseTag+" points to") {
		t.Fatalf("expected wrong-commit tag failure, got:\n%s", combinedOutput)
	}
}

func newReleaseRepo(t *testing.T) releaseRepo {
	t.Helper()

	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("read test working directory: %v", err)
	}

	repo := releaseRepo{
		dir:        t.TempDir(),
		scriptPath: filepath.Join(workingDir, "prepare-github-release.sh"),
	}

	runCommand(t, repo.dir, gitCommand, gitInitCommand, "-q")
	runCommand(t, repo.dir, gitCommand, "checkout", "-q", "-b", "main")
	runCommand(t, repo.dir, gitCommand, gitConfigCommand, "user.email", "ci@example.invalid")
	runCommand(t, repo.dir, gitCommand, gitConfigCommand, "user.name", "CI")

	repo.writeChangelog(t, changelogWithTopRelease("1.0.0", "### Added\n\n- Seed release."))
	repo.commit(t, seedCommitMessage)

	return repo
}

func (repo releaseRepo) writeChangelog(t *testing.T, content string) {
	t.Helper()

	writeFile(t, repo.dir, changelogFile, content)
}

func (repo releaseRepo) commit(t *testing.T, message string) {
	t.Helper()

	runCommand(t, repo.dir, gitCommand, gitAddCommand, ".")
	runCommand(t, repo.dir, gitCommand, "commit", "-q", "-m", message)
}

func (repo releaseRepo) addOriginWithTag(t *testing.T, tag string) {
	t.Helper()

	originDir := t.TempDir()

	runCommand(t, originDir, gitCommand, gitInitCommand, "--bare", "-q")
	runCommand(t, repo.dir, gitCommand, "remote", gitAddCommand, gitOriginRemote, originDir)
	runCommand(t, repo.dir, gitCommand, gitTagCommand, tag)
	runCommand(t, repo.dir, gitCommand, "push", gitOriginRemote, "refs/tags/"+tag)
	runCommand(t, repo.dir, gitCommand, gitTagCommand, "-d", tag)
}

func (repo releaseRepo) addOrigin(t *testing.T) {
	t.Helper()

	originDir := t.TempDir()

	runCommand(t, originDir, gitCommand, gitInitCommand, "--bare", "-q")
	runCommand(t, repo.dir, gitCommand, "remote", gitAddCommand, gitOriginRemote, originDir)
}

func (repo releaseRepo) requirePrepareSuccess(t *testing.T, environment ...string) prepareOutput {
	t.Helper()

	output, combinedOutput, err := repo.runPrepare(t, environment...)
	if err != nil {
		t.Fatalf("prepare script failed: %v\n%s", err, combinedOutput)
	}

	return output
}

func (repo releaseRepo) requirePrepareFailure(t *testing.T, environment ...string) string {
	t.Helper()

	_, combinedOutput, err := repo.runPrepare(t, environment...)
	if err == nil {
		t.Fatalf("prepare script unexpectedly passed:\n%s", combinedOutput)
	}

	return combinedOutput
}

func (repo releaseRepo) runPrepare(t *testing.T, environment ...string) (prepareOutput, string, error) {
	t.Helper()

	outputPath := filepath.Join(repo.dir, githubOutputFile)
	ctx, cancel := context.WithTimeout(context.Background(), scriptTimeout)
	defer cancel()

	//nolint:gosec // The test executes a repository-local script against a temp git repository.
	command := exec.CommandContext(ctx, "bash", repo.scriptPath)
	command.Dir = repo.dir
	command.Env = append(os.Environ(), "GITHUB_OUTPUT="+outputPath, "RUNNER_TEMP="+repo.dir)
	command.Env = append(command.Env, environment...)

	combinedOutputBytes, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("prepare script timed out: %v", ctx.Err())
	}

	output := readPrepareOutput(t, outputPath)
	combinedOutput := string(combinedOutputBytes)

	return output, combinedOutput, err
}

func validatePREnvironment(title string) []string {
	return []string{
		envAssignment(releaseModeEnv, modeValidatePR),
		envAssignment(releasePRTitleEnv, title),
	}
}

func envAssignment(name string, value string) string {
	return name + "=" + value
}

func readPrepareOutput(t *testing.T, outputPath string) prepareOutput {
	t.Helper()

	//nolint:gosec // The path is generated inside the test's temp repository.
	outputBytes, err := os.ReadFile(outputPath)
	if os.IsNotExist(err) {
		return prepareOutput{}
	}
	if err != nil {
		t.Fatalf("read GitHub output file: %v", err)
	}

	return parsePrepareOutput(string(outputBytes))
}

func parsePrepareOutput(content string) prepareOutput {
	var output prepareOutput

	lines := strings.Split(strings.TrimSpace(content), "\n")
	for _, line := range lines {
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}

		switch key {
		case outputBodyFile:
			output.bodyFile = value
		case outputDate:
			output.date = value
		case outputRelease:
			output.releaseDetected = value
		case outputTag:
			output.tag = value
		case outputTagExists:
			output.tagExists = value
		}
	}

	return output
}

func requireReleaseBody(t *testing.T, bodyFile string, want string) {
	t.Helper()

	//nolint:gosec // The path comes from the test-owned release helper output.
	bodyBytes, err := os.ReadFile(bodyFile)
	if err != nil {
		t.Fatalf("read release body file: %v", err)
	}

	got := strings.TrimSpace(string(bodyBytes))
	if got != want {
		t.Fatalf("unexpected release body:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func requireOutputValue(t *testing.T, got string, want string, name string) {
	t.Helper()

	if got != want {
		t.Fatalf("unexpected %s: got %q want %q", name, got, want)
	}
}

func changelogWithTopRelease(version string, body string) string {
	return fmt.Sprintf(`# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

## [%s] - %s

%s

## [0.9.0] - 2026-04-01

### Added

- Older release.
`, version, releaseDate, body)
}

func changelogWithMalformedTopRelease(body string) string {
	return fmt.Sprintf(`# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

## [1.1] - %s

%s

## [0.9.0] - 2026-04-01

### Added

- Older release.
`, releaseDate, body)
}

func writeFile(t *testing.T, dir string, name string, content string) {
	t.Helper()

	path := filepath.Join(dir, name)
	err := os.WriteFile(path, []byte(content), 0o600)
	if err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func removeFile(t *testing.T, dir string, name string) {
	t.Helper()

	path := filepath.Join(dir, name)
	err := os.Remove(path)
	if err != nil {
		t.Fatalf("remove %s: %v", name, err)
	}
}

func runCommand(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), scriptTimeout)
	defer cancel()

	command := exec.CommandContext(ctx, name, args...)
	command.Dir = dir

	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("%s timed out: %v", name, ctx.Err())
	}
	if err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, output)
	}

	return string(output)
}
