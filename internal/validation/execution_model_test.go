package validation

import (
	"testing"

	"github.com/tobythehutt/anyguard/internal/benchtest"
	"golang.org/x/tools/go/analysis"
)

const (
	errValidateAnyUsageFromFile       = "validate any usage from file: %v"
	errUnexpectedSnapshotCount        = "unexpected package snapshot count: got %d want %d"
	errExpectedSingleRepoValidation   = "expected one repo-wide validation across package passes, got %d"
	errUnexpectedPerPackageDiagnostic = "unexpected per-package analyzer diagnostics: got %d want %d"
	errUnexpectedDiagnosticTotal      = "unexpected analyzer diagnostic total across package passes: got %d want %d"
)

func TestValidateAnyUsageAuditsWholeRepo(t *testing.T) {
	fixture := benchtest.CreateSyntheticRepo(t, benchtest.SyntheticRepoConfig{
		PackageCount: 4,
		SafeFiles:    1,
		UsageFiles:   1,
	})

	violations, err := ValidateAnyUsageFromFile(fixture.AllowlistPath, fixture.Root, fixture.Roots)
	if err != nil {
		t.Fatalf(errValidateAnyUsageFromFile, err)
	}
	if got, want := len(violations), fixture.ExpectedViolations; got != want {
		t.Fatalf("unexpected repo-wide audit violation count: got %d want %d", got, want)
	}
}

func TestAnalyzerRunUsesRepoWideAllowlistValidation(t *testing.T) {
	resetProcessRepoValidationCacheForTesting()
	t.Cleanup(resetProcessRepoValidationCacheForTesting)

	fixture := benchtest.CreateSyntheticRepo(t, benchtest.SyntheticRepoConfig{
		PackageCount: 4,
		SafeFiles:    1,
		UsageFiles:   1,
	})
	snapshots := benchtest.LoadPackageSnapshots(t, fixture.Root, fixture.PackagePatterns)
	if got, want := len(snapshots), fixture.Stats.Packages; got != want {
		t.Fatalf(errUnexpectedSnapshotCount, got, want)
	}

	cfg := testSyntheticAnalyzerConfig(fixture)
	pass := benchtest.NewPass(snapshots[0], NewAnalyzer(), nil)
	if _, err := cfg.run(pass); err != nil {
		t.Fatalf("expected repo-wide allowlist validation for analyzer path: %v", err)
	}
}

func TestAnalyzerRunReportsPackageLocalDiagnosticsAndReusesRepoValidation(t *testing.T) {
	resetProcessRepoValidationCacheForTesting()
	t.Cleanup(resetProcessRepoValidationCacheForTesting)

	fixture := benchtest.CreateSyntheticRepo(t, benchtest.SyntheticRepoConfig{
		PackageCount: 4,
		SafeFiles:    1,
		UsageFiles:   1,
	})
	snapshots := benchtest.LoadPackageSnapshots(t, fixture.Root, fixture.PackagePatterns)
	if got, want := len(snapshots), fixture.Stats.Packages; got != want {
		t.Fatalf(errUnexpectedSnapshotCount, got, want)
	}

	auditViolations, err := ValidateAnyUsageFromFile(fixture.AllowlistPath, fixture.Root, fixture.Roots)
	if err != nil {
		t.Fatalf(errValidateAnyUsageFromFile, err)
	}

	if fixture.ExpectedViolations%fixture.Stats.Packages != 0 {
		t.Fatalf(
			"expected evenly distributed fixture diagnostics: violations=%d packages=%d",
			fixture.ExpectedViolations,
			fixture.Stats.Packages,
		)
	}
	expectedPerPackage := fixture.ExpectedViolations / fixture.Stats.Packages

	cfg := testSyntheticAnalyzerConfig(fixture)
	var cache repoValidationCache
	repoValidationCalls := 0
	cfg.loadRepoValidation = func(
		repoRoot string,
		roots []string,
		allowlist AnyAllowlist,
		allowlistFingerprint string,
	) (repoValidationResult, error) {
		buildCtx := currentBuildContext()
		key := newRepoValidationCacheKey(
			repoRoot,
			roots,
			allowlistFingerprint,
			allowlist.ExcludeGlobs,
			buildContextCacheKey(buildCtx),
		)
		return cache.load(key, func() (repoValidationResult, error) {
			repoValidationCalls++
			return collectRepoValidationResultWithBuildContext(repoRoot, roots, allowlist, buildCtx)
		})
	}

	totalDiagnostics := 0
	for _, snapshot := range snapshots {
		totalDiagnostics += testRunAnalyzerDiagnostics(t, cfg, snapshot)
	}

	if repoValidationCalls != 1 {
		t.Fatalf(errExpectedSingleRepoValidation, repoValidationCalls)
	}
	// Re-run a representative package after the aggregate pass to assert that
	// analyzer diagnostics stay package-local and stable independent of cache reuse.
	if got := testRunAnalyzerDiagnostics(t, cfg, snapshots[0]); got != expectedPerPackage {
		t.Fatalf(errUnexpectedPerPackageDiagnostic, got, expectedPerPackage)
	}
	if got, want := totalDiagnostics, len(auditViolations); got != want {
		t.Fatalf(errUnexpectedDiagnosticTotal, got, want)
	}
}

func testSyntheticAnalyzerConfig(fixture benchtest.SyntheticRepo) *analyzerConfig {
	return &analyzerConfig{
		allowlistPath: fixture.AllowlistRelPath,
		repoRoot:      fixture.Root,
		roots:         DefaultRoots,
	}
}

func testRunAnalyzerDiagnostics(tb testing.TB, cfg *analyzerConfig, snapshot benchtest.PackageSnapshot) int {
	tb.Helper()

	diagnosticCount := 0
	pass := benchtest.NewPass(snapshot, NewAnalyzer(), func(analysis.Diagnostic) {
		diagnosticCount++
	})
	if _, err := cfg.run(pass); err != nil {
		tb.Fatalf("run analyzer: %v", err)
	}
	return diagnosticCount
}
