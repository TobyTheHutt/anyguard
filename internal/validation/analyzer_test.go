package validation

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
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
	testPkgDir         = "pkg"
	testNilPkgPath     = "pkg/nilpkg.go"
	testAnalyzerSrc    = "package pkg\ntype T map[string]any\n"
	testCreateSource   = "create source dir: %v"
	testWriteSource    = "write source file: %v"
	testParseSource    = "parse source: %v"
	testUnexpectedErr  = "unexpected error: %v"
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

func TestCollectAnalyzerFilesWithNilPackage(t *testing.T) {
	base := t.TempDir()
	sourcePath := filepath.Join(base, testNilPkgPath)
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o750); err != nil {
		t.Fatalf(testCreateSource, err)
	}
	if err := os.WriteFile(sourcePath, []byte(testAnalyzerSrc), 0o600); err != nil {
		t.Fatalf(testWriteSource, err)
	}

	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, sourcePath, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf(testParseSource, err)
	}

	pass := &analysis.Pass{
		Fset:  fset,
		Files: []*ast.File{parsed},
	}

	files, err := collectAnalyzerFiles(pass, base, []string{DefaultRoots})
	if err != nil {
		t.Fatalf("collect files: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected one file, got %d", len(files))
	}
	if got, want := files[0].relPath, testNilPkgPath; got != want {
		t.Fatalf("unexpected relative path: got %q want %q", got, want)
	}
}

func TestCollectAnalyzerFilesRejectsAmbiguousIdentity(t *testing.T) {
	repoRoot := t.TempDir()
	gopathRoot := filepath.Join(t.TempDir(), "src", "example.com", "project")
	sourcePath := filepath.Join(gopathRoot, testPkgDir, "outside.go")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o750); err != nil {
		t.Fatalf(testCreateSource, err)
	}
	if err := os.WriteFile(sourcePath, []byte(testAnalyzerSrc), 0o600); err != nil {
		t.Fatalf(testWriteSource, err)
	}

	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, sourcePath, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf(testParseSource, err)
	}

	pass := &analysis.Pass{
		Fset:  fset,
		Files: []*ast.File{parsed},
	}

	_, err = collectAnalyzerFiles(pass, repoRoot, []string{DefaultRoots})
	if err == nil {
		t.Fatalf("expected canonical path resolution error")
	}
	if !strings.Contains(err.Error(), "cannot establish canonical repository-relative path") {
		t.Fatalf(testUnexpectedErr, err)
	}
}

func setAnalyzerFlag(t *testing.T, analyzer *analysis.Analyzer, key, value string) {
	t.Helper()
	if err := analyzer.Flags.Set(key, value); err != nil {
		t.Fatalf("set %s: %v", key, err)
	}
}
