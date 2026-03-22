package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/tobythehutt/anyguard/v2/internal/benchtest"
	"golang.org/x/tools/go/analysis"
)

const errUnexpectedSmokeDiagnosticCount = "unexpected smoke diagnostic count: got %d want %d"
const expectedSmokeDiagnosticCount = 5

func BenchmarkModulePluginSmokePath(b *testing.B) {
	smokeRoot := benchtest.CopyModuleTree(b, filepath.Join("..", "testdata", "golangci", "smoke"))
	patterns := smokePackagePatterns(b, smokeRoot)
	snapshots := benchtest.LoadPackageSnapshots(b, smokeRoot, patterns)
	settings := map[string]any{
		flagAllowlist: "any_allowlist.yaml",
		flagRepoRoot:  smokeRoot,
		flagRoots:     []any{"./..."},
	}

	expectedDiagnostics := benchmarkSmokeDiagnostics(b, settings, snapshots)
	// The checked-in smoke fixture currently emits five diagnostics, matching the
	// shell smoke script assertions under scripts/ci/run-golangci-plugin-smoke.sh.
	if got, want := expectedDiagnostics, expectedSmokeDiagnosticCount; got != want {
		b.Fatalf(errUnexpectedSmokeDiagnosticCount, got, want)
	}

	name := fmt.Sprintf("smoke-%dpkgs-%ddiagnostics", len(snapshots), expectedDiagnostics)
	b.Run(name, func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if got := benchmarkSmokeDiagnostics(b, settings, snapshots); got != expectedDiagnostics {
				b.Fatalf(errUnexpectedSmokeDiagnosticCount, got, expectedDiagnostics)
			}
		}
	})
}

func benchmarkSmokeDiagnostics(tb testing.TB, settings map[string]any, snapshots []benchtest.PackageSnapshot) int {
	tb.Helper()

	pluginInstance, err := New(settings)
	if err != nil {
		tb.Fatalf("new plugin: %v", err)
	}

	analyzers, err := pluginInstance.BuildAnalyzers()
	if err != nil {
		tb.Fatalf(errBuildAnalyzers, err)
	}
	if len(analyzers) != 1 {
		tb.Fatalf(errExpectedOneAnalyzer, len(analyzers))
	}

	diagnosticCount := 0
	for _, snapshot := range snapshots {
		pass := benchtest.NewPass(snapshot, analyzers[0], func(analysis.Diagnostic) {
			diagnosticCount++
		})
		_, runErr := analyzers[0].Run(pass)
		if runErr != nil {
			tb.Fatalf("run analyzer: %v", runErr)
		}
	}
	return diagnosticCount
}

func smokePackagePatterns(tb testing.TB, smokeRoot string) []string {
	tb.Helper()

	entries, err := os.ReadDir(filepath.Join(smokeRoot, "pkg"))
	if err != nil {
		tb.Fatalf("read smoke packages: %v", err)
	}

	patterns := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			patterns = append(patterns, "./pkg/"+entry.Name())
		}
	}
	sort.Strings(patterns)
	return patterns
}
