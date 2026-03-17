package validation

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
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

func TestCollectAnalyzerViolationsSortsAcrossFileOrder(t *testing.T) {
	fset := token.NewFileSet()
	alphaFile := parseAnalyzerTokenFile(t, fset, testAlphaPayloadPath, testAlphaPayloadSource)
	zetaFile := parseAnalyzerTokenFile(t, fset, testZetaLaterPath, testZetaLaterSource)

	files := []analyzerFile{
		{relPath: testZetaLaterPath, tokenFile: zetaFile},
		{relPath: testAlphaPayloadPath, tokenFile: alphaFile},
	}
	findings := []collectedFinding{
		{identity: newFindingIdentity(testZetaLaterPath, testOwnerLater, anyCategoryCallExprFun), line: 2, column: 31},
		{identity: newFindingIdentity(testAlphaPayloadPath, testOwnerPayload, anyCategoryMapTypeValue), line: 2, column: 22},
		{identity: newFindingIdentity(testAlphaPayloadPath, testOwnerPayload, anyCategoryMapTypeKey), line: 2, column: 18},
		{identity: newFindingIdentity(testZetaLaterPath, testOwnerLater, anyCategoryCallExprFun), line: 2, column: 23},
	}

	reportable := collectAnalyzerViolations(files, findings, anyAllowlistIndex{})
	got := make([]violationSummary, 0, len(reportable))
	for _, entry := range reportable {
		got = append(got, violationSummary{
			file:     entry.violation.Identity.File,
			owner:    entry.violation.Identity.Owner,
			category: entry.violation.Identity.Category,
			line:     entry.violation.Line,
			column:   entry.violation.Column,
		})
	}

	want := []violationSummary{
		{file: testAlphaPayloadPath, owner: testOwnerPayload, category: string(anyCategoryMapTypeKey), line: 2, column: 18},
		{file: testAlphaPayloadPath, owner: testOwnerPayload, category: string(anyCategoryMapTypeValue), line: 2, column: 22},
		{file: testZetaLaterPath, owner: testOwnerLater, category: string(anyCategoryCallExprFun), line: 2, column: 23},
		{file: testZetaLaterPath, owner: testOwnerLater, category: string(anyCategoryCallExprFun), line: 2, column: 31},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected analyzer violation order:\ngot: %#v\nwant: %#v", got, want)
	}
}

func TestReportViolationUsesLineAndColumn(t *testing.T) {
	fset := token.NewFileSet()
	tokenFile := parseAnalyzerTokenFile(t, fset, "pkg/report.go", "package pkg\nfunc Use() { _ = any(1) }\n")

	var reported analysis.Diagnostic
	pass := &analysis.Pass{
		Fset: fset,
		Report: func(diagnostic analysis.Diagnostic) {
			reported = diagnostic
		},
	}

	reportViolation(pass, tokenFile, Error{
		File:    "pkg/report.go",
		Line:    2,
		Column:  18,
		Message: "test diagnostic",
		Identity: FindingIdentity{
			File:     "pkg/report.go",
			Owner:    "Use",
			Category: string(anyCategoryCallExprFun),
		},
	})

	if reported.Pos == token.NoPos {
		t.Fatalf("expected reported diagnostic position")
	}
	position := fset.PositionFor(reported.Pos, false)
	if position.Line != 2 || position.Column != 18 {
		t.Fatalf("unexpected diagnostic position: line=%d column=%d", position.Line, position.Column)
	}
}

func setAnalyzerFlag(t *testing.T, analyzer *analysis.Analyzer, key, value string) {
	t.Helper()
	if err := analyzer.Flags.Set(key, value); err != nil {
		t.Fatalf("set %s: %v", key, err)
	}
}

func parseAnalyzerTokenFile(t *testing.T, fset *token.FileSet, name, src string) *token.File {
	t.Helper()

	parsed, err := parser.ParseFile(fset, name, src, parser.ParseComments)
	if err != nil {
		t.Fatalf(testParseSource, err)
	}

	tokenFile := fset.File(parsed.Package)
	if tokenFile == nil {
		t.Fatalf("expected token file for %s", name)
	}
	return tokenFile
}
