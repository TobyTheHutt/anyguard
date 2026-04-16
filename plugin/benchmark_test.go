package plugin

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/tobythehutt/anyguard/v2/internal/benchtest"
	"golang.org/x/tools/go/analysis"
)

const errUnexpectedSmokeDiagnosticCount = "unexpected smoke diagnostic count: got %d want %d"
const expectedSmokeDiagnosticCount = 5

type smokeBenchmarkCase struct {
	name     string
	loadMode benchtest.PackageLoadMode
}

func BenchmarkModulePluginSmokePath(b *testing.B) {
	smokeRoot := benchtest.CopyModuleTree(b, filepath.Join("..", "testdata", "golangci", "smoke"))
	patterns := smokePackagePatterns(b, smokeRoot)
	pluginSettings := map[string]any{
		flagAllowlist: "any_allowlist.yaml",
		flagRepoRoot:  smokeRoot,
		flagRoots:     []any{"./..."},
	}

	benchmarkCases := []smokeBenchmarkCase{
		{name: "syntax-load", loadMode: benchtest.PackageLoadModeSyntax},
		{name: "typesinfo-load-baseline", loadMode: benchtest.PackageLoadModeTypesInfo},
	}
	for _, benchmarkCase := range benchmarkCases {
		benchmarkModulePluginSmokePath(b, smokeRoot, patterns, pluginSettings, benchmarkCase)
	}
}

func benchmarkModulePluginSmokePath(
	b *testing.B,
	smokeRoot string,
	patterns []string,
	settings map[string]any,
	benchmarkCase smokeBenchmarkCase,
) {
	b.Helper()

	snapshots := loadSmokeSnapshots(b, smokeRoot, patterns, benchmarkCase.loadMode)
	diagnosticCount := benchmarkSmokeDiagnostics(b, settings, snapshots)
	expectedDiagnosticCount := expectedSmokeDiagnosticCount
	// The checked-in smoke fixture emits five diagnostics. The shell smoke script
	// checks the same count.
	if diagnosticCount != expectedDiagnosticCount {
		b.Fatalf(errUnexpectedSmokeDiagnosticCount, diagnosticCount, expectedDiagnosticCount)
	}

	snapshotCount := len(snapshots)
	benchmarkName := fmt.Sprintf("%s-%dpkgs-%ddiagnostics", benchmarkCase.name, snapshotCount, diagnosticCount)
	b.Run(benchmarkName, func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			// Each iteration reloads package inputs. The benchmark includes the
			// package work selected by the module-plugin load mode.
			reloadedSnapshots := loadSmokeSnapshots(b, smokeRoot, patterns, benchmarkCase.loadMode)
			reloadedDiagnosticCount := benchmarkSmokeDiagnostics(b, settings, reloadedSnapshots)
			if reloadedDiagnosticCount != expectedDiagnosticCount {
				b.Fatalf(errUnexpectedSmokeDiagnosticCount, reloadedDiagnosticCount, expectedDiagnosticCount)
			}
		}
	})
}

// loadSmokeSnapshots mirrors the module-plugin split. Both modes parse files.
// The typesinfo baseline adds type checking on top of that parse step.
func loadSmokeSnapshots(tb testing.TB, smokeRoot string, patterns []string, loadMode benchtest.PackageLoadMode) []benchtest.PackageSnapshot {
	tb.Helper()

	goVersion := loadSmokeGoVersion(tb, smokeRoot)
	snapshotCount := len(patterns)
	snapshots := make([]benchtest.PackageSnapshot, 0, snapshotCount)
	for _, pattern := range patterns {
		snapshot := loadSmokeSnapshot(tb, smokeRoot, pattern, loadMode, goVersion)
		snapshots = append(snapshots, snapshot)
	}
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].Path < snapshots[j].Path
	})
	return snapshots
}

func loadSmokeSnapshot(
	tb testing.TB,
	smokeRoot string,
	pattern string,
	loadMode benchtest.PackageLoadMode,
	goVersion string,
) benchtest.PackageSnapshot {
	tb.Helper()

	packagePath := strings.TrimPrefix(pattern, "./")
	normalizedPackagePath := filepath.FromSlash(packagePath)
	packageDir := filepath.Join(smokeRoot, normalizedPackagePath)
	fset := token.NewFileSet()
	syntaxFiles := parseSmokePackageFiles(tb, fset, packageDir)
	snapshot := benchtest.PackageSnapshot{
		Fset:     fset,
		Files:    syntaxFiles,
		Path:     pattern,
		ReadFile: os.ReadFile,
	}
	if loadMode != benchtest.PackageLoadModeTypesInfo {
		return snapshot
	}

	typesPkg, typesInfo := typeCheckSmokePackage(tb, fset, pattern, syntaxFiles, goVersion)
	snapshot.Types = typesPkg
	snapshot.TypesInfo = typesInfo
	return snapshot
}

func parseSmokePackageFiles(tb testing.TB, fset *token.FileSet, packageDir string) []*ast.File {
	tb.Helper()

	entries, err := os.ReadDir(packageDir)
	if err != nil {
		tb.Fatalf("read smoke package dir: %v", err)
	}

	files := make([]*ast.File, 0, len(entries))
	for _, entry := range entries {
		entryName := entry.Name()
		isDir := entry.IsDir()
		isGoFile := filepath.Ext(entryName) == ".go"
		if isDir || !isGoFile {
			continue
		}
		filePath := filepath.Join(packageDir, entryName)
		syntax, parseErr := parser.ParseFile(fset, filePath, nil, parser.ParseComments)
		if parseErr != nil {
			tb.Fatalf("parse smoke package file: %v", parseErr)
		}
		files = append(files, syntax)
	}
	if len(files) == 0 {
		tb.Fatalf("expected go files in smoke package dir %q", packageDir)
	}
	return files
}

func typeCheckSmokePackage(
	tb testing.TB,
	fset *token.FileSet,
	packagePath string,
	syntaxFiles []*ast.File,
	goVersion string,
) (*types.Package, *types.Info) {
	tb.Helper()

	const errTypeCheckSmokePackage = "type check smoke package %q: %v"

	packageName := syntaxFiles[0].Name.Name
	typesPkg := types.NewPackage(packagePath, packageName)
	typesInfo := &types.Info{
		Types:        make(map[ast.Expr]types.TypeAndValue),
		Instances:    make(map[*ast.Ident]types.Instance),
		Defs:         make(map[*ast.Ident]types.Object),
		Uses:         make(map[*ast.Ident]types.Object),
		Implicits:    make(map[ast.Node]types.Object),
		Selections:   make(map[*ast.SelectorExpr]*types.Selection),
		Scopes:       make(map[ast.Node]*types.Scope),
		FileVersions: make(map[*ast.File]string),
	}
	defaultImporter := importer.Default()
	typeSizes := types.SizesFor("gc", runtime.GOARCH)
	reportTypeError := func(err error) {
		tb.Fatalf(errTypeCheckSmokePackage, packagePath, err)
	}
	config := &types.Config{
		Importer:  defaultImporter,
		Sizes:     typeSizes,
		GoVersion: goVersion,
		Error:     reportTypeError,
	}
	checker := types.NewChecker(config, fset, typesPkg, typesInfo)
	if err := checker.Files(syntaxFiles); err != nil {
		tb.Fatalf(errTypeCheckSmokePackage, packagePath, err)
	}
	return typesPkg, typesInfo
}

func loadSmokeGoVersion(tb testing.TB, smokeRoot string) string {
	tb.Helper()

	const goDirectivePrefix = "go "

	goModPath := filepath.Join(smokeRoot, goModFileName)
	// #nosec G304 -- goModPath is rooted under the copied benchmark fixture tree.
	data, err := os.ReadFile(goModPath)
	if err != nil {
		tb.Fatalf("read smoke go.mod: %v", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, goDirectivePrefix) {
			continue
		}
		return "go" + strings.TrimSpace(strings.TrimPrefix(trimmed, goDirectivePrefix))
	}
	tb.Fatalf("resolve smoke Go version from %q", goModPath)
	return ""
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
		countDiagnostic := func(analysis.Diagnostic) {
			diagnosticCount++
		}
		analyzer := analyzers[0]
		pass := benchtest.NewPass(snapshot, analyzer, countDiagnostic)
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
		isDir := entry.IsDir()
		if isDir {
			entryName := entry.Name()
			pattern := "./pkg/" + entryName
			patterns = append(patterns, pattern)
		}
	}
	sort.Strings(patterns)
	return patterns
}
