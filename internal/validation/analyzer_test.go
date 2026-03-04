package validation

import (
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
)

func TestAnalyzer(t *testing.T) {
	analyzer := NewAnalyzer()
	testdata := analysistest.TestData()
	repoRoot := filepath.Join(testdata, "src")

	setAnalyzerFlag(t, analyzer, "allowlist", filepath.Join(testdata, "allowlist.yaml"))
	setAnalyzerFlag(t, analyzer, "repo-root", repoRoot)
	setAnalyzerFlag(t, analyzer, "roots", "./...")

	analysistest.Run(t, testdata, analyzer, "violations", "allowed")
}

func TestAnalyzerRespectsRoots(t *testing.T) {
	analyzer := NewAnalyzer()
	testdata := analysistest.TestData()
	repoRoot := filepath.Join(testdata, "src")

	setAnalyzerFlag(t, analyzer, "allowlist", filepath.Join(testdata, "allowlist-empty.yaml"))
	setAnalyzerFlag(t, analyzer, "repo-root", repoRoot)
	setAnalyzerFlag(t, analyzer, "roots", "allowed")

	analysistest.Run(t, testdata, analyzer, "filtered")
}

func setAnalyzerFlag(t *testing.T, analyzer *analysis.Analyzer, key, value string) {
	t.Helper()
	if err := analyzer.Flags.Set(key, value); err != nil {
		t.Fatalf("set %s: %v", key, err)
	}
}
