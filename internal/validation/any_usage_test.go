package validation

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestLoadAnyAllowlistErrors(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	if _, err := LoadAnyAllowlist(missing); err == nil {
		t.Fatalf("expected missing-file error")
	}

	path := filepath.Join(t.TempDir(), "allow.yaml")
	if err := os.WriteFile(path, []byte(": bad"), 0o600); err != nil {
		t.Fatalf("write invalid file: %v", err)
	}
	if _, err := LoadAnyAllowlist(path); err == nil {
		t.Fatalf("expected parse error")
	}
}

func TestLoadAnyAllowlistRequiresDescription(t *testing.T) {
	base := t.TempDir()
	allowPath := filepath.Join(base, "allowlist.yaml")
	content := "version: 1\nentries:\n  - path: pkg/api/payload.go\n"
	if err := os.WriteFile(allowPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write allowlist: %v", err)
	}

	if _, err := LoadAnyAllowlist(allowPath); err == nil {
		t.Fatalf("expected missing description error")
	}
}

func TestValidateAnyUsageFromFile(t *testing.T) {
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "pkg", "api", "payload.go"), "package api\ntype Payload map[string]any\n")

	allowlist := AnyAllowlist{
		Version:      1,
		ExcludeGlobs: []string{"**/*_test.go"},
		Entries: []AnyAllowlistEntry{
			{
				Path:        "pkg/api/payload.go",
				Symbols:     []string{"Payload"},
				Description: "payload boundary",
			},
		},
	}
	allowPath := filepath.Join(base, "allowlist.yaml")
	writeAllowlist(t, allowPath, allowlist)

	violations, err := ValidateAnyUsageFromFile(allowPath, base, []string{"pkg/api"})
	if err != nil {
		t.Fatalf("validate usage from file: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %v", violations)
	}
}

func TestValidateAnyUsageDetectsViolation(t *testing.T) {
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "pkg", "api", "payload.go"), "package api\ntype Payload map[string]any\n")

	violations, err := ValidateAnyUsage(AnyAllowlist{Version: 1}, base, []string{"pkg/api"})
	if err != nil {
		t.Fatalf("validate usage: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(violations))
	}
	if violations[0].File != "pkg/api/payload.go" {
		t.Fatalf("unexpected file: %q", violations[0].File)
	}
	if violations[0].Line != 2 {
		t.Fatalf("unexpected line: %d", violations[0].Line)
	}
}

func TestValidateAnyUsageSupportsNolint(t *testing.T) {
	testCases := []struct {
		name string
		src  string
	}{
		{
			name: "same line",
			src:  "package api\ntype Payload map[string]any //nolint:anyguard\n",
		},
		{
			name: "previous line",
			src:  "package api\n//nolint:anyguard\ntype Payload map[string]any\n",
		},
		{
			name: "generic nolint",
			src:  "package api\ntype Payload map[string]any //nolint\n",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			base := t.TempDir()
			writeFile(t, filepath.Join(base, "pkg", "api", "payload.go"), testCase.src)
			violations, err := ValidateAnyUsage(AnyAllowlist{Version: 1}, base, []string{"pkg/api"})
			if err != nil {
				t.Fatalf("validate usage: %v", err)
			}
			if len(violations) != 0 {
				t.Fatalf("expected no violations, got %v", violations)
			}
		})
	}
}

func TestValidateAnyUsageHandlesExcludesAndRoots(t *testing.T) {
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "pkg", "api", "payload.go"), "package api\ntype Payload map[string]any\n")
	writeFile(t, filepath.Join(base, "pkg", "api", "payload_test.go"), "package api\ntype PayloadTest map[string]any\n")

	allowlist := AnyAllowlist{
		Version:      1,
		ExcludeGlobs: []string{"**/*_test.go"},
	}
	violations, err := ValidateAnyUsage(allowlist, base, []string{"./..."})
	if err != nil {
		t.Fatalf("validate usage: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected exactly one violation, got %d", len(violations))
	}
	if violations[0].File != "pkg/api/payload.go" {
		t.Fatalf("unexpected file in violation: %q", violations[0].File)
	}
}

func TestValidateAnyUsageAllowsTypeParamConstraint(t *testing.T) {
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "pkg", "api", "generic.go"), "package api\nfunc Use[T any](v T) {}\n")

	violations, err := ValidateAnyUsage(AnyAllowlist{Version: 1}, base, []string{"pkg/api"})
	if err != nil {
		t.Fatalf("validate usage: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %v", violations)
	}
}

func TestValidateAnyUsageErrorCases(t *testing.T) {
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "pkg", "api", "ok.go"), "package api\n")
	writeFile(t, filepath.Join(base, "pkg", "api", "broken.go"), "package api\nfunc\n")
	plainFile := filepath.Join(base, "plain.go")
	if err := os.WriteFile(plainFile, []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write plain file: %v", err)
	}

	testCases := []struct {
		name      string
		roots     []string
		wantError bool
	}{
		{name: "missing roots", roots: nil, wantError: true},
		{name: "missing path", roots: []string{"does/not/exist"}, wantError: true},
		{name: "non-directory root", roots: []string{plainFile}, wantError: true},
		{name: "invalid go file", roots: []string{"pkg/api"}, wantError: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := ValidateAnyUsage(AnyAllowlist{Version: 1}, base, testCase.roots)
			if (err != nil) != testCase.wantError {
				t.Fatalf("error mismatch: err=%v wantError=%v", err, testCase.wantError)
			}
		})
	}
}

func TestUtilityFunctions(t *testing.T) {
	if normalizeRootValue("./...") != "." {
		t.Fatalf("expected ./... to normalize to .")
	}
	if normalizeRootValue("pkg/api/...") != "pkg/api" {
		t.Fatalf("expected nested pattern root normalization")
	}
	if normalizeRootValue("  ") != "" {
		t.Fatalf("expected blank root to normalize to empty")
	}

	if !matchesAnyguardNolint("//nolint:anyguard // reason") {
		t.Fatalf("expected specific nolint to match")
	}
	if !matchesAnyguardNolint("//nolint // reason") {
		t.Fatalf("expected generic nolint to match")
	}
	if matchesAnyguardNolint("//nolint:gocyclo // reason") {
		t.Fatalf("did not expect unrelated nolint to match")
	}

	if !isSuppressedByNolint(4, map[int]struct{}{3: {}}) {
		t.Fatalf("expected previous line nolint suppression")
	}
	if isSuppressedByNolint(4, map[int]struct{}{2: {}}) {
		t.Fatalf("did not expect suppression from two lines above")
	}
}

func TestShouldExcludeAndGlobMatch(t *testing.T) {
	if !shouldExclude("pkg/api/foo_test.go", []string{"**/*_test.go"}) {
		t.Fatalf("expected exclude match")
	}
	if shouldExclude("pkg/api/foo.go", []string{"**/*_test.go"}) {
		t.Fatalf("did not expect exclude match")
	}
	ok, err := matchGlob("pkg/**/foo*.go", "pkg/api/foo_test.go")
	if err != nil || !ok {
		t.Fatalf("expected recursive glob match, got ok=%v err=%v", ok, err)
	}
}

func TestTypeHelpers(t *testing.T) {
	ident := &ast.Ident{Name: "any"}
	if got := isTypeIdent([]ast.Node{&ast.MapType{Value: ident}, ident}); !got {
		t.Fatalf("expected map value context to be type context")
	}
	if got := isTypeIdent([]ast.Node{ident}); got {
		t.Fatalf("did not expect short stack to be type context")
	}
	if got := receiverTypeName(&ast.StarExpr{X: &ast.Ident{Name: "Host"}}); got != "Host" {
		t.Fatalf("unexpected receiver name: %q", got)
	}

	src := "package p\ntype Box struct{}\nfunc (b *Box) Run(v any) {}\n"
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "sample.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse file: %v", err)
	}
	ranges := buildSymbolRanges(file)
	if symbol := symbolForPos(ranges, token.Pos(1)); symbol != "" {
		t.Fatalf("expected no symbol at beginning, got %q", symbol)
	}
}

func writeAllowlist(t *testing.T, path string, allowlist AnyAllowlist) {
	t.Helper()
	data, err := yaml.Marshal(allowlist)
	if err != nil {
		t.Fatalf("marshal allowlist: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write allowlist: %v", err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
}
