// Package benchtest provides shared benchmark fixtures and analysis-pass helpers.
package benchtest

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/packages"
)

const (
	defaultGoVersion      = "1.22.0"
	defaultModulePath     = "example.com/anyguard-bench"
	defaultAllowlistPath  = "internal/ci/any_allowlist.yaml"
	defaultConfiguredRoot = "./..."
	goModFileName         = "go.mod"
	syntheticNameFormat   = "%s_%02d"
)

const (
	categoryCallExprFun  = "*ast.CallExpr.Fun"
	categoryFieldType    = "*ast.Field.Type"
	categoryMapTypeKey   = "*ast.MapType.Key"
	categoryTypeSpecType = "*ast.TypeSpec.Type"
)

const (
	findingsPerBenchmarkFile  = 11
	selectorsPerBenchmarkFile = 4
)

const packageLoadMode = packages.NeedName |
	packages.NeedFiles |
	packages.NeedCompiledGoFiles |
	packages.NeedImports |
	packages.NeedDeps |
	packages.NeedSyntax |
	packages.NeedTypes |
	packages.NeedTypesInfo

type Selector struct {
	Path     string
	Owner    string
	Category string
}

type RepoStats struct {
	Packages  int
	Files     int
	Findings  int
	Selectors int
}

type SyntheticRepoConfig struct {
	PackageCount int
	SafeFiles    int
	UsageFiles   int
}

type SyntheticRepo struct {
	AllowlistPath         string
	AllowlistRelPath      string
	ExpectedViolations    int
	PackagePatterns       []string
	RepresentativePackage string
	Root                  string
	Roots                 []string
	Selectors             []Selector
	Stats                 RepoStats
}

type PackageSnapshot struct {
	Fset      *token.FileSet
	Files     []*ast.File
	Path      string
	ReadFile  func(string) ([]byte, error)
	Types     *types.Package
	TypesInfo *types.Info
}

func DefaultSyntheticRepoConfig() SyntheticRepoConfig {
	return SyntheticRepoConfig{
		PackageCount: 12,
		SafeFiles:    2,
		UsageFiles:   4,
	}
}

func CreateSyntheticRepo(tb testing.TB, cfg SyntheticRepoConfig) SyntheticRepo {
	tb.Helper()

	cfg = normalizeSyntheticRepoConfig(cfg)
	root := tb.TempDir()
	writeFile(tb, root, goModFileName, fmt.Sprintf("module %s\n\ngo %s\n", defaultModulePath, defaultGoVersion))

	repo := SyntheticRepo{
		AllowlistPath:    filepath.Join(root, filepath.FromSlash(defaultAllowlistPath)),
		AllowlistRelPath: defaultAllowlistPath,
		Root:             root,
		Roots:            []string{defaultConfiguredRoot},
		Selectors:        make([]Selector, 0, cfg.PackageCount*cfg.UsageFiles*selectorsPerBenchmarkFile),
		Stats: RepoStats{
			Packages: cfg.PackageCount,
			Files:    cfg.PackageCount * (cfg.UsageFiles + cfg.SafeFiles),
			Findings: cfg.PackageCount * cfg.UsageFiles * findingsPerBenchmarkFile,
		},
	}

	for pkgIndex := 0; pkgIndex < cfg.PackageCount; pkgIndex++ {
		pkgName := fmt.Sprintf("pkg%02d", pkgIndex)
		pkgDir := filepath.Join("pkg", pkgName)
		pattern := "./" + filepath.ToSlash(pkgDir)
		repo.PackagePatterns = append(repo.PackagePatterns, pattern)
		if pkgIndex == cfg.PackageCount/2 {
			repo.RepresentativePackage = pattern
		}

		for fileIndex := 0; fileIndex < cfg.UsageFiles; fileIndex++ {
			relPath := filepath.ToSlash(filepath.Join(pkgDir, fmt.Sprintf("usage_%02d.go", fileIndex)))
			selectors := writeUsageFile(tb, root, pkgName, relPath, fileIndex)
			repo.Selectors = append(repo.Selectors, selectors...)
		}

		for fileIndex := 0; fileIndex < cfg.SafeFiles; fileIndex++ {
			relPath := filepath.ToSlash(filepath.Join(pkgDir, fmt.Sprintf("safe_%02d.go", fileIndex)))
			writeSafeFile(tb, root, pkgName, relPath, fileIndex)
		}
	}

	sort.Strings(repo.PackagePatterns)
	repo.Stats.Selectors = len(repo.Selectors)
	repo.ExpectedViolations = repo.Stats.Findings - repo.Stats.Selectors
	writeAllowlist(tb, repo.AllowlistPath, repo.Selectors)
	return repo
}

func CopyModuleTree(tb testing.TB, srcRoot string) string {
	tb.Helper()

	dstRoot := tb.TempDir()
	err := filepath.WalkDir(srcRoot, func(path string, entry os.DirEntry, walkErr error) error {
		return copyModuleTreeEntry(srcRoot, dstRoot, path, entry, walkErr)
	})
	if err != nil {
		tb.Fatalf("copy module tree: %v", err)
	}

	rewriteGoDirective(tb, filepath.Join(dstRoot, goModFileName), defaultGoVersion)
	return dstRoot
}

//nolint:gosec // Benchmark fixtures only copy repository-controlled files into t.TempDir.
func copyModuleTreeEntry(srcRoot, dstRoot, path string, entry os.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}

	relPath, relErr := filepath.Rel(srcRoot, path)
	if relErr != nil {
		return relErr
	}
	if relPath == "." {
		return nil
	}

	if entry.IsDir() {
		return os.MkdirAll(filepath.Join(dstRoot, relPath), 0o750)
	}

	dstPath := filepath.Join(dstRoot, relPath)
	if mkdirErr := os.MkdirAll(filepath.Dir(dstPath), 0o750); mkdirErr != nil {
		return mkdirErr
	}

	data, readErr := os.ReadFile(path)
	if readErr != nil {
		return readErr
	}
	return os.WriteFile(dstPath, data, 0o600)
}

func LoadPackageSnapshots(tb testing.TB, dir string, patterns []string) []PackageSnapshot {
	tb.Helper()

	cfg := &packages.Config{
		Dir:  dir,
		Env:  append(os.Environ(), "GOWORK=off"),
		Mode: packageLoadMode,
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		tb.Fatalf("load packages: %v", err)
	}
	if count := packages.PrintErrors(pkgs); count > 0 {
		tb.Fatalf("load packages reported %d errors", count)
	}

	snapshots := make([]PackageSnapshot, 0, len(pkgs))
	for _, pkg := range pkgs {
		if pkg.Types == nil || pkg.TypesInfo == nil || pkg.Fset == nil || len(pkg.Syntax) == 0 {
			tb.Fatalf("package %q missing syntax or type information", pkg.PkgPath)
		}

		snapshots = append(snapshots, PackageSnapshot{
			Fset:      pkg.Fset,
			Files:     pkg.Syntax,
			Path:      pkg.PkgPath,
			ReadFile:  os.ReadFile,
			Types:     pkg.Types,
			TypesInfo: pkg.TypesInfo,
		})
	}

	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].Path < snapshots[j].Path
	})
	return snapshots
}

func NewPass(snapshot PackageSnapshot, analyzer *analysis.Analyzer, report func(analysis.Diagnostic)) *analysis.Pass {
	if report == nil {
		report = func(analysis.Diagnostic) {}
	}
	return &analysis.Pass{
		Analyzer:  analyzer,
		Fset:      snapshot.Fset,
		Files:     snapshot.Files,
		Pkg:       snapshot.Types,
		TypesInfo: snapshot.TypesInfo,
		ReadFile:  snapshot.ReadFile,
		Report:    report,
	}
}

func normalizeSyntheticRepoConfig(cfg SyntheticRepoConfig) SyntheticRepoConfig {
	defaults := DefaultSyntheticRepoConfig()
	if cfg.PackageCount <= 0 {
		cfg.PackageCount = defaults.PackageCount
	}
	if cfg.SafeFiles <= 0 {
		cfg.SafeFiles = defaults.SafeFiles
	}
	if cfg.UsageFiles <= 0 {
		cfg.UsageFiles = defaults.UsageFiles
	}
	return cfg
}

//nolint:gosec // Benchmark fixtures rewrite the copied temporary go.mod file only.
func rewriteGoDirective(tb testing.TB, goModPath, version string) {
	tb.Helper()

	data, err := os.ReadFile(goModPath)
	if err != nil {
		tb.Fatalf("read go.mod: %v", err)
	}

	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "go ") {
			lines[i] = "go " + version
			break
		}
	}

	writeErr := os.WriteFile(goModPath, []byte(strings.Join(lines, "\n")), 0o600)
	if writeErr != nil {
		tb.Fatalf("write go.mod: %v", writeErr)
	}
}

func writeAllowlist(tb testing.TB, path string, selectors []Selector) {
	tb.Helper()

	var builder strings.Builder
	builder.WriteString("version: 2\n")
	builder.WriteString("exclude_globs:\n")
	builder.WriteString("  - \"**/*_test.go\"\n")
	builder.WriteString("entries:\n")
	for _, selector := range selectors {
		builder.WriteString("  - selector:\n")
		fmt.Fprintf(&builder, "      path: %q\n", selector.Path)
		fmt.Fprintf(&builder, "      owner: %q\n", selector.Owner)
		fmt.Fprintf(&builder, "      category: %q\n", selector.Category)
		builder.WriteString("    description: benchmark fixture allowlist entry\n")
	}

	writeFile(tb, filepath.Dir(path), filepath.Base(path), builder.String())
}

func writeUsageFile(tb testing.TB, root, pkgName, relPath string, fileIndex int) []Selector {
	tb.Helper()

	suffix := fmt.Sprintf(syntheticNameFormat, pkgName, fileIndex)
	payloadOwner := "Payload" + suffix
	aliasOwner := "Alias" + suffix
	valueOwner := "Value" + suffix
	useOwner := "Use" + suffix
	singleName := "Single" + suffix
	pairName := "Pair" + suffix
	safeParam := "value"

	var builder strings.Builder
	builder.WriteString("package ")
	builder.WriteString(pkgName)
	builder.WriteString("\n\n")
	fmt.Fprintf(&builder, "type %s map[any][]any\n", payloadOwner)
	fmt.Fprintf(&builder, "type %s = any\n", aliasOwner)
	fmt.Fprintf(&builder, "var %s any\n\n", valueOwner)
	fmt.Fprintf(&builder, "type %s[T any] struct{}\n", singleName)
	fmt.Fprintf(&builder, "type %s[T, U any] struct{}\n\n", pairName)
	fmt.Fprintf(&builder, "func %s(%s any) {\n", useOwner, safeParam)
	builder.WriteString("\tvar local map[string]any\n")
	builder.WriteString("\ttype Hidden = any\n")
	fmt.Fprintf(&builder, "\t_ = any(%s)\n", safeParam)
	fmt.Fprintf(&builder, "\t_ = %s[any]{}\n", singleName)
	fmt.Fprintf(&builder, "\t_ = %s[int, any]{}\n", pairName)
	fmt.Fprintf(&builder, "\t_ = %s.(any)\n", safeParam)
	builder.WriteString("\t_ = local\n")
	builder.WriteString("}\n")

	writeFile(tb, root, relPath, builder.String())
	return []Selector{
		{Path: relPath, Owner: payloadOwner, Category: categoryMapTypeKey},
		{Path: relPath, Owner: aliasOwner, Category: categoryTypeSpecType},
		{Path: relPath, Owner: useOwner, Category: categoryFieldType},
		{Path: relPath, Owner: useOwner, Category: categoryCallExprFun},
	}
}

func writeSafeFile(tb testing.TB, root, pkgName, relPath string, fileIndex int) {
	tb.Helper()

	suffix := fmt.Sprintf(syntheticNameFormat, pkgName, fileIndex)
	content := fmt.Sprintf(`package %s

type Safe%s struct {
	Value string
}

func Keep%s(value string) string {
	return value
}
`, pkgName, suffix, suffix)
	writeFile(tb, root, relPath, content)
}

func writeFile(tb testing.TB, root, relPath, content string) {
	tb.Helper()

	path := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		tb.Fatalf("create %s: %v", relPath, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		tb.Fatalf("write %s: %v", relPath, err)
	}
}
