package validation

import (
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
)

const (
	testdataSrcDir     = "src"
	testAllowlistPath  = "allowlist.yaml"
	testAllowlistEmpty = "allowlist-empty.yaml"
	testPkgViolations  = "violations"
	testPkgAllowed     = "allowed"
	testPkgFiltered    = "filtered"
)

func TestAnalyzer(t *testing.T) {
	analyzer := NewAnalyzer()
	testdata := analysistest.TestData()
	repoRoot := filepath.Join(testdata, testdataSrcDir)

	setAnalyzerFlag(t, analyzer, flagAllowlist, filepath.Join(testdata, testAllowlistPath))
	setAnalyzerFlag(t, analyzer, flagRepoRoot, repoRoot)
	setAnalyzerFlag(t, analyzer, flagRoots, DefaultRoots)

	analysistest.Run(t, testdata, analyzer, testPkgViolations, testPkgAllowed)
}

func TestAnalyzerRespectsRoots(t *testing.T) {
	analyzer := NewAnalyzer()
	testdata := analysistest.TestData()
	repoRoot := filepath.Join(testdata, testdataSrcDir)

	setAnalyzerFlag(t, analyzer, flagAllowlist, filepath.Join(testdata, testAllowlistEmpty))
	setAnalyzerFlag(t, analyzer, flagRepoRoot, repoRoot)
	setAnalyzerFlag(t, analyzer, flagRoots, testPkgAllowed)

	analysistest.Run(t, testdata, analyzer, testPkgFiltered)
}

func setAnalyzerFlag(t *testing.T, analyzer *analysis.Analyzer, key, value string) {
	t.Helper()
	if err := analyzer.Flags.Set(key, value); err != nil {
		t.Fatalf("set %s: %v", key, err)
	}
}
