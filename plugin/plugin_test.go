package plugin

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/golangci/plugin-module-register/register"
	"github.com/tobythehutt/anyguard/v2/internal/benchtest"
	"golang.org/x/tools/go/analysis"
)

const (
	errNewPlugin           = "new plugin: %v"
	errBuildAnalyzers      = "build analyzers: %v"
	errExpectedOneAnalyzer = "expected one analyzer, got %d"
	errRunAnalyzer         = "run analyzer: %v"
	flagAllowlist          = "allowlist"
	flagRepoRoot           = "repo-root"
	flagRoots              = "roots"
	goModFileName          = "go.mod"
	valueRootAll           = "./..."
	valueRootPackage       = "pkg/api"
	valueAnyAllowlist      = "ci/allowlist.yaml"
	valueRepoRoot          = "/repo/root"
	valueRoots             = valueRootAll + "," + valueRootPackage
	syntaxTestRoots        = valueRootAll
	syntaxTestAllowlist    = "allowlist.yaml"
	syntaxTestAllowlistYML = "version: 2\nentries: []\n"
	syntaxTestGoMod        = "module example.com/pluginsyntax\n\ngo 1.22.0\n"
	syntaxTestPackage      = "./pkg"
	syntaxTestSourcePath   = "pkg/payload.go"
	syntaxTestSource       = "package pkg\n\ntype Payload map[string]any\n\nfunc Shadowed(values []int) {\n\tany := 0\n\t_ = values[any]\n}\n"
	syntaxTestLine         = 3
	syntaxTestColumn       = 25
)

func TestInitRegistersPlugin(t *testing.T) {
	newPlugin, err := register.GetPlugin(Name)
	if err != nil {
		t.Fatalf("get plugin: %v", err)
	}

	instance, err := newPlugin(nil)
	if err != nil {
		t.Fatalf("create plugin: %v", err)
	}
	if instance == nil {
		t.Fatalf("expected plugin instance")
	}
}

func TestNewRejectsUnknownSettings(t *testing.T) {
	_, err := New(map[string]any{"unknown": true})
	if err == nil {
		t.Fatalf("expected decode error")
	}
}

func TestModulePluginBuildAnalyzers(t *testing.T) {
	pluginInstance, err := New(map[string]any{
		flagAllowlist: valueAnyAllowlist,
		flagRepoRoot:  valueRepoRoot,
		flagRoots:     []any{valueRootAll, valueRootPackage},
	})
	if err != nil {
		t.Fatalf(errNewPlugin, err)
	}

	analyzers, err := pluginInstance.BuildAnalyzers()
	if err != nil {
		t.Fatalf(errBuildAnalyzers, err)
	}
	if len(analyzers) != 1 {
		t.Fatalf(errExpectedOneAnalyzer, len(analyzers))
	}

	analyzer := analyzers[0]
	if got, want := analyzer.Name, Name; got != want {
		t.Fatalf("unexpected analyzer name: got %q want %q", got, want)
	}

	if got, want := analyzer.Flags.Lookup(flagAllowlist).Value.String(), valueAnyAllowlist; got != want {
		t.Fatalf("allowlist flag mismatch: got %q want %q", got, want)
	}
	if got, want := analyzer.Flags.Lookup(flagRepoRoot).Value.String(), valueRepoRoot; got != want {
		t.Fatalf("repo-root flag mismatch: got %q want %q", got, want)
	}
	if got, want := analyzer.Flags.Lookup(flagRoots).Value.String(), valueRoots; got != want {
		t.Fatalf("roots flag mismatch: got %q want %q", got, want)
	}
}

func TestModulePluginGetLoadMode(t *testing.T) {
	pluginInstance, err := New(nil)
	if err != nil {
		t.Fatalf(errNewPlugin, err)
	}

	if got, want := pluginInstance.GetLoadMode(), register.LoadModeSyntax; got != want {
		t.Fatalf("unexpected load mode: got %q want %q", got, want)
	}
}

// This test locks the plugin to passes with parsed files and no type data.
func TestModulePluginRunsOnSyntaxPass(t *testing.T) {
	repoRoot := t.TempDir()
	allowlistPath := syntaxTestAllowlist
	sourcePath := syntaxTestSourcePath
	packagePatterns := []string{syntaxTestPackage}
	loadMode := benchtest.PackageLoadModeSyntax

	writePluginFixtureFile(t, repoRoot, goModFileName, syntaxTestGoMod)
	writePluginFixtureFile(t, repoRoot, allowlistPath, syntaxTestAllowlistYML)
	writePluginFixtureFile(t, repoRoot, sourcePath, syntaxTestSource)

	snapshots := benchtest.LoadPackageSnapshotsWithMode(
		t,
		repoRoot,
		packagePatterns,
		loadMode,
	)
	snapshotCount := len(snapshots)
	expectedSnapshotCount := 1
	if snapshotCount != expectedSnapshotCount {
		t.Fatalf("unexpected snapshot count: got %d want %d", snapshotCount, expectedSnapshotCount)
	}

	snapshot := snapshots[0]
	if snapshot.Types != nil {
		t.Fatalf("expected syntax snapshot to leave package types nil")
	}
	if snapshot.TypesInfo != nil {
		t.Fatalf("expected syntax snapshot to leave types info nil")
	}

	settings := map[string]any{
		flagAllowlist: allowlistPath,
		flagRepoRoot:  repoRoot,
		flagRoots:     []any{syntaxTestRoots},
	}
	pluginInstance, err := New(settings)
	if err != nil {
		t.Fatalf(errNewPlugin, err)
	}

	analyzers, err := pluginInstance.BuildAnalyzers()
	if err != nil {
		t.Fatalf(errBuildAnalyzers, err)
	}
	analyzerCount := len(analyzers)
	expectedAnalyzerCount := 1
	if analyzerCount != expectedAnalyzerCount {
		t.Fatalf(errExpectedOneAnalyzer, analyzerCount)
	}

	analyzer := analyzers[0]
	diagnostics := collectPluginDiagnostics(t, analyzer, snapshot)
	diagnosticCount := len(diagnostics)
	expectedDiagnosticCount := 1
	if diagnosticCount != expectedDiagnosticCount {
		t.Fatalf("unexpected diagnostic count: got %d want %d", diagnosticCount, expectedDiagnosticCount)
	}

	diagnostic := diagnostics[0]
	position := snapshot.Fset.PositionFor(diagnostic.Pos, false)
	expectedFile := filepath.Join(repoRoot, sourcePath)
	gotFile := filepath.ToSlash(position.Filename)
	wantFile := filepath.ToSlash(expectedFile)
	if gotFile != wantFile {
		t.Fatalf("unexpected diagnostic file: got %q want %q", gotFile, wantFile)
	}
	gotLine := position.Line
	wantLine := syntaxTestLine
	if gotLine != wantLine {
		t.Fatalf("unexpected diagnostic line: got %d want %d", gotLine, wantLine)
	}
	gotColumn := position.Column
	wantColumn := syntaxTestColumn
	if gotColumn != wantColumn {
		t.Fatalf("unexpected diagnostic column: got %d want %d", gotColumn, wantColumn)
	}
}

func TestRootsSettingUnmarshal(t *testing.T) {
	testCases := []struct {
		name string
		raw  any
		want []string
	}{
		{
			name: "list",
			raw:  map[string]any{"roots": []any{valueRootAll, " " + valueRootPackage + " ", ""}},
			want: []string{valueRootAll, valueRootPackage},
		},
		{
			name: "csv",
			raw:  map[string]any{"roots": " " + valueRootAll + ", " + valueRootPackage + " "},
			want: []string{valueRootAll, valueRootPackage},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			pluginInstance, err := New(testCase.raw)
			if err != nil {
				t.Fatalf(errNewPlugin, err)
			}

			modulePlugin, ok := pluginInstance.(*ModulePlugin)
			if !ok {
				t.Fatalf("unexpected plugin type: %T", pluginInstance)
			}
			if got := []string(modulePlugin.settings.Roots); !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("roots mismatch: got %v want %v", got, testCase.want)
			}
		})
	}
}

func collectPluginDiagnostics(t *testing.T, analyzer *analysis.Analyzer, snapshot benchtest.PackageSnapshot) []analysis.Diagnostic {
	t.Helper()

	diagnostics := make([]analysis.Diagnostic, 0, 1)
	recordDiagnostic := func(diagnostic analysis.Diagnostic) {
		diagnostics = append(diagnostics, diagnostic)
	}
	pass := benchtest.NewPass(snapshot, analyzer, recordDiagnostic)
	if _, err := analyzer.Run(pass); err != nil {
		t.Fatalf(errRunAnalyzer, err)
	}
	return diagnostics
}

func writePluginFixtureFile(t *testing.T, root, relPath, content string) {
	t.Helper()

	normalizedPath := filepath.FromSlash(relPath)
	path := filepath.Join(root, normalizedPath)
	dirPath := filepath.Dir(path)
	if err := os.MkdirAll(dirPath, 0o750); err != nil {
		t.Fatalf("create plugin fixture dir: %v", err)
	}
	data := []byte(content)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write plugin fixture file: %v", err)
	}
}
