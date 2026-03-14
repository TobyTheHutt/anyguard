package validation

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	testDirPkg                 = "pkg"
	testDirAPI                 = "api"
	testRootAPI                = "pkg/api"
	testPayloadPath            = "pkg/api/payload.go"
	testPayloadFile            = "payload.go"
	testAllowlistFile          = "allowlist.yaml"
	testWriteAllowlistErrFmt   = "write allowlist: %v"
	testValidateUsageErrFmt    = "validate usage: %v"
	testNoViolationsErrFmt     = "expected no violations, got %v"
	testFooTestPath            = "pkg/api/foo_test.go"
	testPayloadSource          = "package api\ntype Payload map[string]any\n"
	testExpectedNormalizeRoots = "."
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
	allowPath := filepath.Join(base, testAllowlistFile)
	content := "version: 1\nentries:\n  - path: pkg/api/payload.go\n"
	if err := os.WriteFile(allowPath, []byte(content), 0o600); err != nil {
		t.Fatalf(testWriteAllowlistErrFmt, err)
	}

	if _, err := LoadAnyAllowlist(allowPath); err == nil {
		t.Fatalf("expected missing description error")
	}
}

func TestValidateAnyUsageFromFile(t *testing.T) {
	base := t.TempDir()
	writeFile(t, apiPath(base, testPayloadFile), testPayloadSource)

	allowlist := AnyAllowlist{
		Version:      1,
		ExcludeGlobs: []string{"**/*_test.go"},
		Entries: []AnyAllowlistEntry{
			{
				Path:        testPayloadPath,
				Symbols:     []string{"Payload"},
				Description: "payload boundary",
			},
		},
	}
	allowPath := filepath.Join(base, testAllowlistFile)
	writeAllowlist(t, allowPath, allowlist)

	violations, err := ValidateAnyUsageFromFile(allowPath, base, []string{testRootAPI})
	if err != nil {
		t.Fatalf("validate usage from file: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf(testNoViolationsErrFmt, violations)
	}
}

func TestValidateAnyUsageDetectsViolation(t *testing.T) {
	base := t.TempDir()
	writeFile(t, apiPath(base, testPayloadFile), testPayloadSource)

	violations, err := ValidateAnyUsage(AnyAllowlist{Version: 1}, base, []string{testRootAPI})
	if err != nil {
		t.Fatalf(testValidateUsageErrFmt, err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(violations))
	}
	if violations[0].File != testPayloadPath {
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
			writeFile(t, apiPath(base, testPayloadFile), testCase.src)
			violations, err := ValidateAnyUsage(AnyAllowlist{Version: 1}, base, []string{testRootAPI})
			if err != nil {
				t.Fatalf(testValidateUsageErrFmt, err)
			}
			if len(violations) != 0 {
				t.Fatalf(testNoViolationsErrFmt, violations)
			}
		})
	}
}

func TestValidateAnyUsageHandlesExcludesAndRoots(t *testing.T) {
	base := t.TempDir()
	writeFile(t, apiPath(base, testPayloadFile), testPayloadSource)
	writeFile(t, apiPath(base, "payload_test.go"), "package api\ntype PayloadTest map[string]any\n")

	allowlist := AnyAllowlist{
		Version:      1,
		ExcludeGlobs: []string{"**/*_test.go"},
	}
	violations, err := ValidateAnyUsage(allowlist, base, []string{DefaultRoots})
	if err != nil {
		t.Fatalf(testValidateUsageErrFmt, err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected exactly one violation, got %d", len(violations))
	}
	if violations[0].File != testPayloadPath {
		t.Fatalf("unexpected file in violation: %q", violations[0].File)
	}
}

func TestValidateAnyUsageAllowsTypeParamConstraint(t *testing.T) {
	base := t.TempDir()
	writeFile(t, apiPath(base, "generic.go"), "package api\nfunc Use[T any](v T) {}\ntype Box[T []any] struct{}\n")

	violations, err := ValidateAnyUsage(AnyAllowlist{Version: 1}, base, []string{testRootAPI})
	if err != nil {
		t.Fatalf(testValidateUsageErrFmt, err)
	}
	if len(violations) != 0 {
		t.Fatalf(testNoViolationsErrFmt, violations)
	}
}

func TestValidateAnyUsageErrorCases(t *testing.T) {
	base := t.TempDir()
	writeFile(t, apiPath(base, "ok.go"), "package api\n")
	writeFile(t, apiPath(base, "broken.go"), "package api\nfunc\n")
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
		{name: "invalid go file", roots: []string{testRootAPI}, wantError: true},
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
	if normalizeRootValue(DefaultRoots) != testExpectedNormalizeRoots {
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
	if !shouldExclude(testFooTestPath, []string{"**/*_test.go"}) {
		t.Fatalf("expected exclude match")
	}
	if shouldExclude("pkg/api/foo.go", []string{"**/*_test.go"}) {
		t.Fatalf("did not expect exclude match")
	}
	ok, err := matchGlob("pkg/**/foo*.go", testFooTestPath)
	if err != nil || !ok {
		t.Fatalf("expected recursive glob match, got ok=%v err=%v", ok, err)
	}
}

func TestCollectAnyUsagesCapturesSupportedSlotMetadata(t *testing.T) {
	src := `package p

type Payload map[any][]any
type Alias = any
var Pair, Other any
var Top = func(arg any) []any { return nil }

type Holder[T any] struct{}
func (h *Holder[any]) Run() {}

func Use(value any) {
	var local map[string]any
	type Hidden = any
	_ = any(value)
	_ = values[any]
	_ = Box[int, any]{}
	_ = value.(any)
}

func Match(value any) {
	switch value.(type) {
	case any:
		_ = any(value)
	}
}

type Ignored = (any)

func Generic[T []any](v T) {}
`

	got := collectUsageSummaries(t, src)
	want := []usageSummary{
		{category: anyCategoryMapTypeKey, owner: "Payload", line: 3},
		{category: anyCategoryArrayTypeElt, owner: "Payload", line: 3},
		{category: anyCategoryTypeSpecType, owner: "Alias", line: 4},
		{category: anyCategoryValueSpecType, owner: "Pair", line: 5},
		{category: anyCategoryFieldType, owner: "Top", line: 6},
		{category: anyCategoryArrayTypeElt, owner: "Top", line: 6},
		{category: anyCategoryIndexExprIndex, owner: "Holder", line: 9},
		{category: anyCategoryFieldType, owner: "Use", line: 11},
		{category: anyCategoryMapTypeValue, owner: "Use", line: 12},
		{category: anyCategoryTypeSpecType, owner: "Use", line: 13},
		{category: anyCategoryCallExprFun, owner: "Use", line: 14},
		{category: anyCategoryIndexExprIndex, owner: "Use", line: 15},
		{category: anyCategoryIndexListIndex, owner: "Use", line: 16},
		{category: anyCategoryTypeAssertType, owner: "Use", line: 17},
		{category: anyCategoryFieldType, owner: "Match", line: 20},
		{category: anyCategoryCallExprFun, owner: "Match", line: 23},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected any usages:\ngot: %#v\nwant: %#v", got, want)
	}
}

func TestCollectAnyUsagesTraversesSupportedSlotsInStatements(t *testing.T) {
	src := `package p

type Record struct {
	Field *any
}

type Service interface {
	Handle(any) []any
}

func Explore(value any, ch chan any, values []any) {
label:
	values[any(value)]++
	ch <- any(value)
	go any(value)
	defer any(value)

	if start := any(value); any(value) != nil {
		_ = map[string]any{"k": any(value)}
	} else {
		_ = struct{ Field any }{Field: any(value)}
	}

	holder := struct{ values []int }{values: nil}
	switch any(value) {
	case any(value):
		_ = holder.values[any(value)]
	}

	select {
	case ch <- any(value):
	default:
		_ = values[any(value):any(value):any(value)]
	}

	for index := any(value); any(value) != nil; index = any(value) {
		_ = index
	}

	for _, item := range any(value) {
		_ = item
	}

	if true {
		goto label
	}
}
`

	got := collectUsageSummaries(t, src)
	want := []usageSummary{
		{category: anyCategoryStarExprX, owner: "Record", line: 4},
		{category: anyCategoryFieldType, owner: "Service", line: 8},
		{category: anyCategoryArrayTypeElt, owner: "Service", line: 8},
		{category: anyCategoryFieldType, owner: "Explore", line: 11},
		{category: anyCategoryChanTypeValue, owner: "Explore", line: 11},
		{category: anyCategoryArrayTypeElt, owner: "Explore", line: 11},
		{category: anyCategoryCallExprFun, owner: "Explore", line: 13},
		{category: anyCategoryCallExprFun, owner: "Explore", line: 14},
		{category: anyCategoryCallExprFun, owner: "Explore", line: 15},
		{category: anyCategoryCallExprFun, owner: "Explore", line: 16},
		{category: anyCategoryCallExprFun, owner: "Explore", line: 18},
		{category: anyCategoryCallExprFun, owner: "Explore", line: 18},
		{category: anyCategoryMapTypeValue, owner: "Explore", line: 19},
		{category: anyCategoryCallExprFun, owner: "Explore", line: 19},
		{category: anyCategoryFieldType, owner: "Explore", line: 21},
		{category: anyCategoryCallExprFun, owner: "Explore", line: 21},
		{category: anyCategoryCallExprFun, owner: "Explore", line: 25},
		{category: anyCategoryCallExprFun, owner: "Explore", line: 26},
		{category: anyCategoryCallExprFun, owner: "Explore", line: 27},
		{category: anyCategoryCallExprFun, owner: "Explore", line: 31},
		{category: anyCategoryCallExprFun, owner: "Explore", line: 33},
		{category: anyCategoryCallExprFun, owner: "Explore", line: 33},
		{category: anyCategoryCallExprFun, owner: "Explore", line: 33},
		{category: anyCategoryCallExprFun, owner: "Explore", line: 36},
		{category: anyCategoryCallExprFun, owner: "Explore", line: 36},
		{category: anyCategoryCallExprFun, owner: "Explore", line: 36},
		{category: anyCategoryCallExprFun, owner: "Explore", line: 40},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected statement traversal usages:\ngot: %#v\nwant: %#v", got, want)
	}
}

func TestValidateAnyUsageUsesEnclosingFunctionOwnerForLocalDeclarations(t *testing.T) {
	base := t.TempDir()
	writeFile(t, apiPath(base, testPayloadFile), "package api\nfunc Use() {\n\ttype Hidden = any\n\tvar local map[string]any\n\t_ = func(v any) {}\n}\n")

	allowlist := AnyAllowlist{
		Version: 1,
		Entries: []AnyAllowlistEntry{
			{
				Path:        testPayloadPath,
				Symbols:     []string{"Use"},
				Description: "allow local function-owned findings",
			},
		},
	}

	violations, err := ValidateAnyUsage(allowlist, base, []string{testRootAPI})
	if err != nil {
		t.Fatalf(testValidateUsageErrFmt, err)
	}
	if len(violations) != 0 {
		t.Fatalf(testNoViolationsErrFmt, violations)
	}
}

type usageSummary struct {
	category anyUsageCategory
	owner    string
	line     int
}

func collectUsageSummaries(t *testing.T, src string) []usageSummary {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "sample.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse file: %v", err)
	}

	usages := collectAnyUsages(file)
	summaries := make([]usageSummary, 0, len(usages))
	for _, usage := range usages {
		summaries = append(summaries, usageSummary{
			category: usage.category,
			owner:    usage.owner,
			line:     fset.Position(usage.pos).Line,
		})
	}
	return summaries
}

func writeAllowlist(t *testing.T, path string, allowlist AnyAllowlist) {
	t.Helper()
	data, err := yaml.Marshal(allowlist)
	if err != nil {
		t.Fatalf("marshal allowlist: %v", err)
	}
	writeErr := os.WriteFile(path, data, 0o600)
	if writeErr != nil {
		t.Fatalf(testWriteAllowlistErrFmt, writeErr)
	}
}

func apiPath(base, file string) string {
	return filepath.Join(base, testDirPkg, testDirAPI, file)
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
