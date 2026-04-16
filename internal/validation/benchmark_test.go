package validation

import (
	"fmt"
	"testing"

	"github.com/tobythehutt/anyguard/v2/internal/benchtest"
	"golang.org/x/tools/go/analysis"
)

const (
	errBenchmarkValidateAnyUsage   = "validate any usage: %v"
	errBenchmarkCollectFindings    = "collect findings: %v"
	errBenchmarkResolveAllowlist   = "resolve allowlist index: %v"
	errBenchmarkRunAnalyzer        = "run analyzer: %v"
	errExpectedAnalyzerDiagnostics = "expected analyzer diagnostics for benchmark fixture"
)

func BenchmarkValidateAnyUsage(b *testing.B) {
	fixture := benchtest.CreateSyntheticRepo(b, benchtest.DefaultSyntheticRepoConfig())
	allowlist := benchmarkAllowlist(fixture.Selectors)
	fixtureName := benchmarkFixtureName(fixture.Stats)

	violations, err := ValidateAnyUsage(allowlist, fixture.Root, fixture.Roots)
	if err != nil {
		b.Fatalf(errBenchmarkValidateAnyUsage, err)
	}
	if got, want := len(violations), fixture.ExpectedViolations; got != want {
		b.Fatalf("unexpected violation count: got %d want %d", got, want)
	}

	b.Run(fixtureName, func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			gotViolations, validateErr := ValidateAnyUsage(allowlist, fixture.Root, fixture.Roots)
			if validateErr != nil {
				b.Fatalf(errBenchmarkValidateAnyUsage, validateErr)
			}
			if len(gotViolations) == 0 {
				b.Fatalf("expected violations for benchmark fixture")
			}
		}
	})
}

func BenchmarkCollectFindings(b *testing.B) {
	fixture := benchtest.CreateSyntheticRepo(b, benchtest.DefaultSyntheticRepoConfig())
	allowlist := benchmarkAllowlist(fixture.Selectors)
	fixtureName := benchmarkFixtureName(fixture.Stats)

	findings, err := collectFindings(fixture.Root, fixture.Roots, allowlist.ExcludeGlobs)
	if err != nil {
		b.Fatalf(errBenchmarkCollectFindings, err)
	}
	if got, want := len(findings), fixture.Stats.Findings; got != want {
		b.Fatalf("unexpected finding count: got %d want %d", got, want)
	}

	b.Run(fixtureName, func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			gotFindings, collectErr := collectFindings(fixture.Root, fixture.Roots, allowlist.ExcludeGlobs)
			if collectErr != nil {
				b.Fatalf(errBenchmarkCollectFindings, collectErr)
			}
			if len(gotFindings) == 0 {
				b.Fatalf("expected findings for benchmark fixture")
			}
		}
	})
}

func BenchmarkExcludeGlobMatching(b *testing.B) {
	globs := benchmarkExcludeGlobs()
	paths := benchmarkExcludePaths()
	compiledGlobs, err := compileExcludeGlobs(globs)
	if err != nil {
		b.Fatalf("compile benchmark exclude globs: %v", err)
	}

	b.Run("old-compile-regex-per-path", func(b *testing.B) {
		b.ReportAllocs()
		benchmarkExcludeMatchCount = benchmarkExcludeMatching(b, paths, func(path string) bool {
			return benchmarkShouldExcludeUncompiled(path, globs)
		})
	})

	b.Run("new-reuse-compiled-regex", func(b *testing.B) {
		b.ReportAllocs()
		benchmarkExcludeMatchCount = benchmarkExcludeMatching(b, paths, func(path string) bool {
			return shouldExclude(path, compiledGlobs)
		})
	})
}

func BenchmarkResolveAllowlistIndex(b *testing.B) {
	fixture := benchtest.CreateSyntheticRepo(b, benchtest.DefaultSyntheticRepoConfig())
	allowlist := benchmarkAllowlist(fixture.Selectors)
	findings, err := collectFindings(fixture.Root, fixture.Roots, allowlist.ExcludeGlobs)
	if err != nil {
		b.Fatalf(errBenchmarkCollectFindings, err)
	}

	index, err := resolveAllowlistIndex(allowlist, findings)
	if err != nil {
		b.Fatalf(errBenchmarkResolveAllowlist, err)
	}
	if got, want := len(index.allowed), fixture.Stats.Selectors; got != want {
		b.Fatalf("unexpected allowlist size: got %d want %d", got, want)
	}

	b.Run(benchmarkFixtureName(fixture.Stats), func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			resolvedIndex, resolveErr := resolveAllowlistIndex(allowlist, findings)
			if resolveErr != nil {
				b.Fatalf(errBenchmarkResolveAllowlist, resolveErr)
			}
			if len(resolvedIndex.allowed) == 0 {
				b.Fatalf("expected allowlist index entries")
			}
		}
	})
}

func BenchmarkAnalyzerRun(b *testing.B) {
	fixture := benchtest.CreateSyntheticRepo(b, benchtest.DefaultSyntheticRepoConfig())
	fixtureName := benchmarkFixtureName(fixture.Stats)
	cfg := &analyzerConfig{
		allowlistPath: fixture.AllowlistRelPath,
		repoRoot:      fixture.Root,
		roots:         DefaultRoots,
	}

	resetProcessRepoValidationCacheForTesting()
	preloadedSnapshot := loadRepresentativeSnapshot(b, fixture)
	if count := benchmarkAnalyzerDiagnostics(b, cfg, preloadedSnapshot); count == 0 {
		b.Fatal(errExpectedAnalyzerDiagnostics)
	}
	resetProcessRepoValidationCacheForTesting()

	b.Run(fixtureName+"/cold-pass", func(b *testing.B) {
		benchmarkAnalyzerRunCold(b, cfg, fixture)
	})

	b.Run(fixtureName+"/reused-pass", func(b *testing.B) {
		benchmarkAnalyzerRunReused(b, cfg, preloadedSnapshot)
	})

	b.Run(fixtureName+"/reused-pass-reset-cache", func(b *testing.B) {
		benchmarkAnalyzerRunReusedResetCache(b, cfg, preloadedSnapshot)
	})
}

func benchmarkAllowlist(selectors []benchtest.Selector) AnyAllowlist {
	entries := make([]AnyAllowlistEntry, 0, len(selectors))
	for _, selector := range selectors {
		entries = append(entries, AnyAllowlistEntry{
			Selector: &AnyAllowlistSelector{
				Path:     selector.Path,
				Owner:    selector.Owner,
				Category: selector.Category,
				Line:     selector.Line,
				Column:   selector.Column,
			},
			Description: "benchmark fixture allowlist entry",
		})
	}
	return AnyAllowlist{
		Version:      anyAllowlistVersion,
		ExcludeGlobs: []string{"**/*_test.go"},
		Entries:      entries,
	}
}

func benchmarkFixtureName(stats benchtest.RepoStats) string {
	return fmt.Sprintf(
		"realistic-%dpkgs-%dfiles-%dfindings-%dselectors",
		stats.Packages,
		stats.Files,
		stats.Findings,
		stats.Selectors,
	)
}

func benchmarkAnalyzerDiagnostics(tb testing.TB, cfg *analyzerConfig, snapshot benchtest.PackageSnapshot) int {
	tb.Helper()

	diagnosticCount := 0
	pass := benchtest.NewPass(snapshot, NewAnalyzer(), func(analysis.Diagnostic) {
		diagnosticCount++
	})
	if _, err := cfg.run(pass); err != nil {
		tb.Fatalf(errBenchmarkRunAnalyzer, err)
	}
	return diagnosticCount
}

func loadRepresentativeSnapshot(tb testing.TB, fixture benchtest.SyntheticRepo) benchtest.PackageSnapshot {
	tb.Helper()

	snapshots := benchtest.LoadPackageSnapshots(tb, fixture.Root, []string{fixture.RepresentativePackage})
	if len(snapshots) != 1 {
		tb.Fatalf("expected one representative package, got %d", len(snapshots))
	}
	return snapshots[0]
}

func benchmarkAnalyzerRunCold(b *testing.B, cfg *analyzerConfig, fixture benchtest.SyntheticRepo) {
	b.Helper()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		resetProcessRepoValidationCacheForTesting()
		snapshot := loadRepresentativeSnapshot(b, fixture)
		if count := benchmarkAnalyzerDiagnostics(b, cfg, snapshot); count == 0 {
			b.Fatal(errExpectedAnalyzerDiagnostics)
		}
	}
}

func benchmarkAnalyzerRunReused(b *testing.B, cfg *analyzerConfig, snapshot benchtest.PackageSnapshot) {
	b.Helper()
	b.ReportAllocs()

	resetProcessRepoValidationCacheForTesting()

	// Reuse the same prepared pass across iterations to isolate repeated in-process
	// analyzer execution. pass.Report is reassigned each loop before cfg.run.
	pass := benchtest.NewPass(snapshot, NewAnalyzer(), nil)
	warmDiagnostics := 0
	pass.Report = func(analysis.Diagnostic) {
		warmDiagnostics++
	}
	if _, err := cfg.run(pass); err != nil {
		b.Fatalf(errBenchmarkRunAnalyzer, err)
	}
	if warmDiagnostics == 0 {
		b.Fatal(errExpectedAnalyzerDiagnostics)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		diagnosticCount := 0
		pass.Report = func(analysis.Diagnostic) {
			diagnosticCount++
		}
		if _, err := cfg.run(pass); err != nil {
			b.Fatalf(errBenchmarkRunAnalyzer, err)
		}
		if diagnosticCount == 0 {
			b.Fatal(errExpectedAnalyzerDiagnostics)
		}
	}
}

func benchmarkAnalyzerRunReusedResetCache(b *testing.B, cfg *analyzerConfig, snapshot benchtest.PackageSnapshot) {
	b.Helper()
	b.ReportAllocs()

	// Reset the process-wide repo cache each loop to preserve an uncached baseline
	// alongside the warm repeated-pass measurement above.
	pass := benchtest.NewPass(snapshot, NewAnalyzer(), nil)
	for i := 0; i < b.N; i++ {
		resetProcessRepoValidationCacheForTesting()

		diagnosticCount := 0
		pass.Report = func(analysis.Diagnostic) {
			diagnosticCount++
		}
		if _, err := cfg.run(pass); err != nil {
			b.Fatalf(errBenchmarkRunAnalyzer, err)
		}
		if diagnosticCount == 0 {
			b.Fatal(errExpectedAnalyzerDiagnostics)
		}
	}
}

var benchmarkExcludeMatchCount int

func benchmarkExcludeGlobs() []string {
	return []string{
		"**/*_test.go",
		"pkg/**/generated?.go",
		"pkg/**/fixture*.go",
		"internal/tmp/**",
		"cmd/*/mock?.go",
	}
}

func benchmarkExcludePaths() []string {
	return []string{
		"pkg/api/foo.go",
		"pkg/api/foo_test.go",
		"pkg/api/internal/generated1.go",
		"pkg/service/fixture_alpha.go",
		"internal/tmp/cache/value.go",
		"cmd/anyguard/mock1.go",
		"cmd/anyguard/main.go",
	}
}

func benchmarkExcludeMatching(b *testing.B, paths []string, match func(string) bool) int {
	b.Helper()

	matches := 0
	for i := 0; i < b.N; i++ {
		pathIndex := i % len(paths)
		path := paths[pathIndex]
		if match(path) {
			matches++
		}
	}
	return matches
}

func benchmarkShouldExcludeUncompiled(relPath string, globs []string) bool {
	for _, glob := range globs {
		if glob == "" {
			continue
		}

		matched, err := matchGlob(glob, relPath)
		if err == nil && matched {
			return true
		}
	}
	return false
}
