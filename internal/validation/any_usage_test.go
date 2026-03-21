package validation

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
	testPayloadBoundaryDesc    = "payload boundary"
	testWriteAllowlistErrFmt   = "write allowlist: %v"
	testUnexpectedErrFmt       = "unexpected error: %v"
	testValidateUsageErrFmt    = "validate usage: %v"
	testNoViolationsErrFmt     = "expected no violations, got %v"
	testFooTestPath            = "pkg/api/foo_test.go"
	testOwnerPayload           = "Payload"
	testOwnerUse               = "Use"
	testOwnerLater             = "Later"
	testOwnerAlpha             = "Alpha"
	testOwnerBeta              = "Beta"
	testOwnerZulu              = "Zulu"
	testSamplePath             = "sample.go"
	testPackageAPISource       = "package api\n"
	testPayloadTestFile        = "payload_test.go"
	testBrokenFile             = "broken.go"
	testBrokenGoSource         = "package api\nfunc\n"
	testParseFileErrFmt        = "parse file: %v"
	testExpectedParseError     = "expected parse error"
	testPayloadSource          = "package api\ntype Payload map[string]any\n"
	testAlphaPayloadPath       = "pkg/alpha/payload.go"
	testAlphaPayloadSource     = "package alpha\ntype Payload map[any]any\n"
	testZetaLaterPath          = "pkg/zeta/later.go"
	testZetaLaterSource        = "package zeta\nfunc Later() { _, _ = any(1), any(2) }\n"
	testExpectedNormalizeRoots = "."
	testUnexpectedDeclUsages   = "unexpected declaration-slot usages:\ngot: %#v\nwant: %#v"
	testUnexpectedCompUsages   = "unexpected composite-slot usages:\ngot: %#v\nwant: %#v"
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
		t.Fatal(testExpectedParseError)
	}
}

func TestLoadAnyAllowlistRequiresDescription(t *testing.T) {
	base := t.TempDir()
	allowPath := filepath.Join(base, testAllowlistFile)
	content := "version: 2\nentries:\n  - selector:\n      path: pkg/api/payload.go\n      owner: Payload\n      category: \"*ast.MapType.Value\"\n"
	if err := os.WriteFile(allowPath, []byte(content), 0o600); err != nil {
		t.Fatalf(testWriteAllowlistErrFmt, err)
	}

	if _, err := LoadAnyAllowlist(allowPath); err == nil {
		t.Fatalf("expected missing description error")
	}
}

func TestLoadAnyAllowlistRejectsLegacyEntryShape(t *testing.T) {
	base := t.TempDir()
	allowPath := filepath.Join(base, testAllowlistFile)
	content := "version: 2\nentries:\n  - path: pkg/api/payload.go\n    symbols:\n      - Payload\n    description: legacy owner-wide allow\n"
	if err := os.WriteFile(allowPath, []byte(content), 0o600); err != nil {
		t.Fatalf(testWriteAllowlistErrFmt, err)
	}

	_, err := LoadAnyAllowlist(allowPath)
	if err == nil {
		t.Fatalf("expected legacy entry shape error")
	}
	if !strings.Contains(err.Error(), "legacy allowlist entry shape is unsupported") {
		t.Fatalf(testUnexpectedErrFmt, err)
	}
}

func TestValidateAnyUsageFromFile(t *testing.T) {
	base := t.TempDir()
	writeFile(t, apiPath(base, testPayloadFile), testPayloadSource)

	allowlist := AnyAllowlist{
		Version:      anyAllowlistVersion,
		ExcludeGlobs: []string{"**/*_test.go"},
		Entries: []AnyAllowlistEntry{
			allowlistEntry(testPayloadPath, testOwnerPayload, anyCategoryMapTypeValue, testPayloadBoundaryDesc),
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

	violations, err := ValidateAnyUsage(AnyAllowlist{Version: anyAllowlistVersion}, base, []string{testRootAPI})
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
	if violations[0].Column != 25 {
		t.Fatalf("unexpected column: %d", violations[0].Column)
	}
	if got, want := violations[0].Identity, newFindingIdentity(testPayloadPath, testOwnerPayload, anyCategoryMapTypeValue); got != want {
		t.Fatalf("unexpected identity: got %#v want %#v", got, want)
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
			violations, err := ValidateAnyUsage(AnyAllowlist{Version: anyAllowlistVersion}, base, []string{testRootAPI})
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
	writeFile(t, apiPath(base, testPayloadTestFile), "package api\ntype PayloadTest map[string]any\n")

	allowlist := AnyAllowlist{
		Version:      anyAllowlistVersion,
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

	violations, err := ValidateAnyUsage(AnyAllowlist{Version: anyAllowlistVersion}, base, []string{testRootAPI})
	if err != nil {
		t.Fatalf(testValidateUsageErrFmt, err)
	}
	if len(violations) != 0 {
		t.Fatalf(testNoViolationsErrFmt, violations)
	}
}

func TestValidateAnyUsageIgnoresPackageShadowedAnyAcrossFiles(t *testing.T) {
	base := t.TempDir()
	writeFile(t, apiPath(base, "defs.go"), "package api\ntype any interface{}\ntype Single[T any] struct{}\ntype Box[T, U any] struct{}\n")
	writeFile(
		t,
		apiPath(base, "uses.go"),
		"package api\n"+
			"func FieldType(value any) {}\n"+
			"var ValueSpec any\n"+
			"type TypeSpec = any\n"+
			"type Payload map[string]any\n"+
			"func Use(value interface{}) {\n"+
			"\tvar local any\n"+
			"\ttype Hidden = any\n"+
			"\t_ = value.(any)\n"+
			"\t_ = any(1)\n"+
			"\t_ = Single[any]{}\n"+
			"\t_ = Box[int, any]{}\n"+
			"\t_ = local\n"+
			"}\n",
	)

	violations, err := ValidateAnyUsage(AnyAllowlist{Version: anyAllowlistVersion}, base, []string{testRootAPI})
	if err != nil {
		t.Fatalf(testValidateUsageErrFmt, err)
	}
	if len(violations) != 0 {
		t.Fatalf(testNoViolationsErrFmt, violations)
	}
}

func TestCollectAnyUsagesDeclarationSlotsReportPredeclaredAny(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []usageSummary
	}{
		{
			name: "predeclared any",
			src: `package p

func FieldType(value any) {}

var ValueSpec any

type TypeSpec = any

func TypeAssert(value interface{}) {
	_ = value.(any)
}
`,
			want: []usageSummary{
				{category: anyCategoryFieldType, owner: "FieldType", line: 3},
				{category: anyCategoryValueSpecType, owner: "ValueSpec", line: 5},
				{category: anyCategoryTypeSpecType, owner: "TypeSpec", line: 7},
				{category: anyCategoryTypeAssertType, owner: "TypeAssert", line: 10},
			},
		},
		{
			name: "local predeclared any",
			src: `package p

func Local(value interface{}) {
	var local any
	type LocalAlias = any
	_ = local
	_ = value.(any)
	_ = func(item any) {}
}
`,
			want: []usageSummary{
				{category: anyCategoryValueSpecType, owner: "Local", line: 4},
				{category: anyCategoryTypeSpecType, owner: "Local", line: 5},
				{category: anyCategoryTypeAssertType, owner: "Local", line: 7},
				{category: anyCategoryFieldType, owner: "Local", line: 8},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := collectUsageSummaries(t, tt.src); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf(testUnexpectedDeclUsages, got, tt.want)
			}
		})
	}
}

func TestCollectAnyUsagesDeclarationSlotsIgnoreNonPredeclaredAny(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []usageSummary
	}{
		{
			name: "user defined any type",
			src: `package p

type any int

func FieldType(value any) {}

var ValueSpec any

type TypeSpec = any

func TypeAssert(value interface{}) {
	_ = value.(any)
}
`,
			want: []usageSummary{},
		},
		{
			name: "user defined any alias",
			src: `package p

type any = string

func FieldType(value any) {}

var ValueSpec any

type TypeSpec = any

func TypeAssert(value interface{}) {
	_ = value.(any)
}
`,
			want: []usageSummary{},
		},
		{
			name: "package qualifier named any",
			src: `package p

import any "io"

func FieldType(value any.Reader) {}

var ValueSpec any.Reader

type TypeSpec = any.Reader

func TypeAssert(value interface{}) {
	_ = value.(any.Reader)
}
`,
			want: []usageSummary{},
		},
		{
			name: "local type named any",
			src: `package p

func Local(value interface{}) {
	type any int

	var local any
	type LocalAlias = any
	_ = local
	_ = value.(any)
	_ = func(item any) {}
}
`,
			want: []usageSummary{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := collectUsageSummaries(t, tt.src); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf(testUnexpectedDeclUsages, got, tt.want)
			}
		})
	}
}

func TestCollectAnyUsagesCompositeTypeSlotsReportPredeclaredAny(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []usageSummary
	}{
		{
			name: "predeclared any",
			src: `package p

type ArrayAlias = []any
type MapKeyAlias = map[any]string
type MapValueAlias = map[string]any
type ChanAlias = chan any
type StarAlias = *any

type NestedArrayAlias = map[string][]any
type NestedMapAlias = []map[string]any

func EllipsisAlias(values ...any) {}
func NestedEllipsisAlias(values ...[]any) {}
`,
			want: []usageSummary{
				{category: anyCategoryArrayTypeElt, owner: "ArrayAlias", line: 3},
				{category: anyCategoryMapTypeKey, owner: "MapKeyAlias", line: 4},
				{category: anyCategoryMapTypeValue, owner: "MapValueAlias", line: 5},
				{category: anyCategoryChanTypeValue, owner: "ChanAlias", line: 6},
				{category: anyCategoryStarExprX, owner: "StarAlias", line: 7},
				{category: anyCategoryArrayTypeElt, owner: "NestedArrayAlias", line: 9},
				{category: anyCategoryMapTypeValue, owner: "NestedMapAlias", line: 10},
				{category: anyCategoryEllipsisElt, owner: "EllipsisAlias", line: 12},
				{category: anyCategoryArrayTypeElt, owner: "NestedEllipsisAlias", line: 13},
			},
		},
		{
			name: "local predeclared any",
			src: `package p

func Local() {
	var array []any
	var keyed map[any]string
	var valued map[string]any
	var stream chan any
	var ptr *any
	_, _, _, _, _ = array, keyed, valued, stream, ptr
	_ = func(values ...any) {}
}
`,
			want: []usageSummary{
				{category: anyCategoryArrayTypeElt, owner: "Local", line: 4},
				{category: anyCategoryMapTypeKey, owner: "Local", line: 5},
				{category: anyCategoryMapTypeValue, owner: "Local", line: 6},
				{category: anyCategoryChanTypeValue, owner: "Local", line: 7},
				{category: anyCategoryStarExprX, owner: "Local", line: 8},
				{category: anyCategoryEllipsisElt, owner: "Local", line: 10},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := collectUsageSummaries(t, tt.src); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf(testUnexpectedCompUsages, got, tt.want)
			}
		})
	}
}

func TestCollectAnyUsagesCompositeTypeSlotsIgnoreNonPredeclaredAny(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []usageSummary
	}{
		{
			name: "user defined any type",
			src: `package p

type any int

type ArrayAlias = []any
type MapKeyAlias = map[any]string
type MapValueAlias = map[string]any
type ChanAlias = chan any
type StarAlias = *any

type NestedArrayAlias = map[string][]any
type NestedMapAlias = []map[string]any

func EllipsisAlias(values ...any) {}
func NestedEllipsisAlias(values ...[]any) {}
`,
			want: []usageSummary{},
		},
		{
			name: "user defined any alias",
			src: `package p

type any = string

type ArrayAlias = []any
type MapKeyAlias = map[any]string
type MapValueAlias = map[string]any
type ChanAlias = chan any
type StarAlias = *any

func EllipsisAlias(values ...any) {}
`,
			want: []usageSummary{},
		},
		{
			name: "package qualifier named any",
			src: `package p

import any "io"

type ArrayAlias = []any.Reader
type MapKeyAlias = map[any.Reader]string
type MapValueAlias = map[string]any.Reader
type ChanAlias = chan any.Reader
type StarAlias = *any.Reader

func EllipsisAlias(values ...any.Reader) {}
`,
			want: []usageSummary{},
		},
		{
			name: "local type named any",
			src: `package p

func Local() {
	type any int

	var array []any
	var keyed map[any]string
	var valued map[string]any
	var stream chan any
	var ptr *any
	_, _, _, _, _ = array, keyed, valued, stream, ptr
	_ = func(values ...any) {}
}
`,
			want: []usageSummary{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := collectUsageSummaries(t, tt.src); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf(testUnexpectedCompUsages, got, tt.want)
			}
		})
	}
}

func TestVisitCompositeTypeAnySlotIgnoresNilExpr(t *testing.T) {
	collector := anyUsageCollector{
		file: testSamplePath,
		info: &types.Info{Uses: make(map[*ast.Ident]types.Object)},
	}
	collector.visitCompositeTypeAnySlot(anyCategoryArrayTypeElt, "Owner", nil)
	if len(collector.usages) != 0 {
		t.Fatalf("expected nil composite slot to stay quiet, got %#v", collector.usages)
	}
}

func TestValidateAnyUsageErrorCases(t *testing.T) {
	base := t.TempDir()
	writeFile(t, apiPath(base, "ok.go"), testPackageAPISource)
	writeFile(t, apiPath(base, testBrokenFile), testBrokenGoSource)
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
			_, err := ValidateAnyUsage(AnyAllowlist{Version: anyAllowlistVersion}, base, testCase.roots)
			if (err != nil) != testCase.wantError {
				t.Fatalf("error mismatch: err=%v wantError=%v", err, testCase.wantError)
			}
		})
	}
}

func TestValidateAnyUsageRejectsUnknownSelectorCategory(t *testing.T) {
	base := t.TempDir()
	writeFile(t, apiPath(base, testPayloadFile), testPayloadSource)

	allowlist := AnyAllowlist{
		Version: anyAllowlistVersion,
		Entries: []AnyAllowlistEntry{
			{
				Selector: &AnyAllowlistSelector{
					Path:     testPayloadPath,
					Owner:    testOwnerPayload,
					Category: "not-a-real-category",
				},
				Description: "invalid selector category",
			},
		},
	}

	_, err := ValidateAnyUsage(allowlist, base, []string{testRootAPI})
	if err == nil {
		t.Fatalf("expected unknown category error")
	}
	if !strings.Contains(err.Error(), "unknown category") {
		t.Fatalf(testUnexpectedErrFmt, err)
	}
}

func TestValidateAnyUsageRejectsDuplicateSelectors(t *testing.T) {
	base := t.TempDir()
	writeFile(t, apiPath(base, testPayloadFile), testPayloadSource)

	selector := allowlistEntry(testPayloadPath, testOwnerPayload, anyCategoryMapTypeValue, testPayloadBoundaryDesc)
	allowlist := AnyAllowlist{
		Version: anyAllowlistVersion,
		Entries: []AnyAllowlistEntry{
			selector,
			selector,
		},
	}

	_, err := ValidateAnyUsage(allowlist, base, []string{testRootAPI})
	if err == nil {
		t.Fatalf("expected duplicate selector error")
	}
	if !strings.Contains(err.Error(), "resolve to the same selector") {
		t.Fatalf(testUnexpectedErrFmt, err)
	}
}

func TestValidateAnyUsageRejectsUnresolvedSelector(t *testing.T) {
	base := t.TempDir()
	writeFile(t, apiPath(base, testPayloadFile), testPayloadSource)

	allowlist := AnyAllowlist{
		Version: anyAllowlistVersion,
		Entries: []AnyAllowlistEntry{
			allowlistEntry(testPayloadPath, "Paylod", anyCategoryMapTypeValue, "typoed owner"),
		},
	}

	_, err := ValidateAnyUsage(allowlist, base, []string{testRootAPI})
	if err == nil {
		t.Fatalf("expected unresolved selector error")
	}
	if !strings.Contains(err.Error(), "does not match any finding") {
		t.Fatalf(testUnexpectedErrFmt, err)
	}
}

func TestValidateAnyUsageRejectsMalformedSelector(t *testing.T) {
	base := t.TempDir()
	writeFile(t, apiPath(base, testPayloadFile), testPayloadSource)

	allowlist := AnyAllowlist{
		Version: anyAllowlistVersion,
		Entries: []AnyAllowlistEntry{
			{
				Selector: &AnyAllowlistSelector{
					Path: testPayloadPath,
				},
				Description: "missing owner and category",
			},
		},
	}

	_, err := ValidateAnyUsage(allowlist, base, []string{testRootAPI})
	if err == nil {
		t.Fatalf("expected malformed selector error")
	}
	if !strings.Contains(err.Error(), "selector missing owner") {
		t.Fatalf(testUnexpectedErrFmt, err)
	}
}

func TestUtilityFunctions(t *testing.T) {
	if normalizeRootValue(DefaultRoots) != testExpectedNormalizeRoots {
		t.Fatalf("expected ./... to normalize to .")
	}
	if normalizeRootValue("pkg/api/...") != testRootAPI {
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

type Single[T any] struct{}
type Box[T, U any] struct{}

func Use(value any) {
	var local map[string]any
	type Hidden = any
	_ = any(value)
	_ = Single[any]{}
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

func TestCollectAnyUsagesReportsAmbiguousSlotsForUniverseAnyAlias(t *testing.T) {
	src := `package p

type Single[T any] struct{}
type Box[T, U any] struct{}

func Use(value any) {
	_ = any(value)
	_ = Single[any]{}
	_ = Box[int, any]{}
}
`

	got := collectUsageSummaries(t, src)
	want := []usageSummary{
		{category: anyCategoryFieldType, owner: "Use", line: 6},
		{category: anyCategoryCallExprFun, owner: "Use", line: 7},
		{category: anyCategoryIndexExprIndex, owner: "Use", line: 8},
		{category: anyCategoryIndexListIndex, owner: "Use", line: 9},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected ambiguous-slot usages:\ngot: %#v\nwant: %#v", got, want)
	}
}

func TestResolvesToPredeclaredAny(t *testing.T) {
	tests := []struct {
		name string
		src  string
		line int
		want bool
	}{
		{
			name: "universe alias in field type",
			src: `package p

func Use(value any) {}
`,
			line: 3,
			want: true,
		},
		{
			name: "universe alias in conversion callee",
			src: `package p

func Use(value any) {
	_ = any(value)
}
`,
			line: 4,
			want: true,
		},
		{
			name: "user defined any declaration",
			src: `package p

type any interface{}
`,
			line: 3,
			want: false,
		},
		{
			name: "user defined any use",
			src: `package p

type any interface{}
type Payload map[string]any
`,
			line: 4,
			want: false,
		},
		{
			name: "user defined any alias use",
			src: `package p

type any = string
type Payload map[string]any
`,
			line: 4,
			want: false,
		},
		{
			name: "package qualifier named any",
			src: `package p

import any "io"

var _ any.Reader
`,
			line: 5,
			want: false,
		},
		{
			name: "local type named any",
			src: `package p

func Use() {
	type any int
	var _ any
}
`,
			line: 5,
			want: false,
		},
		{
			name: "shadowed function any call",
			src: `package p

func any(v int) int { return v }

func Use() {
	_ = any(1)
}
`,
			line: 6,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, testSamplePath, tt.src, parser.ParseComments)
			if err != nil {
				t.Fatalf(testParseFileErrFmt, err)
			}

			info := typeCheckTestFile(fset, file)
			ident := findAnyIdentOnLine(t, fset, file, tt.line)
			if got := resolvesToPredeclaredAny(ident, info); got != tt.want {
				t.Fatalf("resolvesToPredeclaredAny(line %d) = %t, want %t", tt.line, got, tt.want)
			}
		})
	}
}

func TestResolvesToPredeclaredAnyWithoutTypeInfo(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, testSamplePath, "package p\n\nfunc Use(value any) {}\n", parser.ParseComments)
	if err != nil {
		t.Fatalf(testParseFileErrFmt, err)
	}

	ident := findAnyIdentOnLine(t, fset, file, 3)
	if resolvesToPredeclaredAny(ident, nil) {
		t.Fatal("expected nil type info to stay quiet")
	}
}

func TestCollectAnyUsagesIgnoresShadowedAnyAcrossSupportedSlots(t *testing.T) {
	src := `package p

type any interface{}
type Payload map[string]any
type Single[T any] struct{}
type Box[T, U any] struct{}

func Use() {
	_ = any(1)
	_ = Single[any]{}
	_ = Box[int, any]{}
}
`

	got := collectUsageSummaries(t, src)
	if len(got) != 0 {
		t.Fatalf("expected shadowed any to stay quiet, got %#v", got)
	}
}

func TestCollectAnyUsagesIgnoresUnsupportedPositions(t *testing.T) {
	src := `package p

func Use[T any](value T) {
	_ = value
}

type Box[T any] struct {
	Value T
}

func TypeSwitchCaseList(value interface{}) {
	switch value.(type) {
	case any, string:
	}
}

func IdentifierNamedAny(any int) int {
	holder := struct{ any int }{any: any}
	_ = []int{any}
	_ = map[int]int{any: any}

	slot := 0
	slot = any

	_ = holder.any
	return any + slot
}

const text = "any in a string should stay quiet"

// any in a comment should stay quiet.
`

	got := collectUsageSummaries(t, src)
	if len(got) != 0 {
		t.Fatalf("expected unsupported positions to stay quiet, got %#v", got)
	}
}

func TestCollectAnyUsagesIgnoresShadowedFunctionAndIndexVariable(t *testing.T) {
	src := `package p

func any(v int) int {
	return v
}

func ShadowedCall() {
	_ = any(1)
}

func ShadowedIndex(values []int) int {
	any := 0
	return values[any]
}
`

	got := collectUsageSummaries(t, src)
	if len(got) != 0 {
		t.Fatalf("expected shadowed function and index variable to stay quiet, got %#v", got)
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

func TestValidateAnyUsageCarriesCanonicalFindingIdentity(t *testing.T) {
	base := t.TempDir()
	writeFile(t, apiPath(base, testPayloadFile), "package api\ntype Payload map[string]any\nfunc Use(value any) {\n\ttype Hidden = any\n\tvar local map[string]any\n\t_ = any(value)\n}\n")

	violations, err := ValidateAnyUsage(AnyAllowlist{Version: anyAllowlistVersion}, base, []string{testRootAPI})
	if err != nil {
		t.Fatalf(testValidateUsageErrFmt, err)
	}

	got := make([]violationSummary, 0, len(violations))
	for _, violation := range violations {
		got = append(got, violationSummary{
			file:     violation.Identity.File,
			owner:    violation.Identity.Owner,
			category: violation.Identity.Category,
			line:     violation.Line,
			column:   violation.Column,
		})
	}
	want := []violationSummary{
		{file: testPayloadPath, owner: testOwnerPayload, category: string(anyCategoryMapTypeValue), line: 2, column: 25},
		{file: testPayloadPath, owner: testOwnerUse, category: string(anyCategoryFieldType), line: 3, column: 16},
		{file: testPayloadPath, owner: testOwnerUse, category: string(anyCategoryTypeSpecType), line: 4, column: 16},
		{file: testPayloadPath, owner: testOwnerUse, category: string(anyCategoryMapTypeValue), line: 5, column: 23},
		{file: testPayloadPath, owner: testOwnerUse, category: string(anyCategoryCallExprFun), line: 6, column: 6},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected violation identities:\ngot: %#v\nwant: %#v", got, want)
	}
}

func TestValidateAnyUsageSortsViolationsDeterministicallyAcrossRoots(t *testing.T) {
	base := t.TempDir()
	writeFile(t, filepath.Join(base, testZetaLaterPath), testZetaLaterSource)
	writeFile(t, filepath.Join(base, testAlphaPayloadPath), testAlphaPayloadSource)

	allowlist := AnyAllowlist{Version: anyAllowlistVersion}

	gotReversed, err := ValidateAnyUsage(allowlist, base, []string{"pkg/zeta", "pkg/alpha"})
	if err != nil {
		t.Fatalf(testValidateUsageErrFmt, err)
	}

	gotCanonical, err := ValidateAnyUsage(allowlist, base, []string{"pkg/alpha", "pkg/zeta"})
	if err != nil {
		t.Fatalf(testValidateUsageErrFmt, err)
	}

	want := []violationSummary{
		{file: testAlphaPayloadPath, owner: testOwnerPayload, category: string(anyCategoryMapTypeKey), line: 2, column: 18},
		{file: testAlphaPayloadPath, owner: testOwnerPayload, category: string(anyCategoryMapTypeValue), line: 2, column: 22},
		{file: testZetaLaterPath, owner: testOwnerLater, category: string(anyCategoryCallExprFun), line: 2, column: 23},
		{file: testZetaLaterPath, owner: testOwnerLater, category: string(anyCategoryCallExprFun), line: 2, column: 31},
	}

	if got := collectViolationSummaries(gotReversed); !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected reversed-root ordering:\ngot: %#v\nwant: %#v", got, want)
	}
	if got := collectViolationSummaries(gotCanonical); !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected canonical-root ordering:\ngot: %#v\nwant: %#v", got, want)
	}
}

func TestCollectFindingsSortsDeterministicallyAcrossRoots(t *testing.T) {
	base := t.TempDir()
	writeFile(t, filepath.Join(base, testZetaLaterPath), testZetaLaterSource)
	writeFile(t, filepath.Join(base, testAlphaPayloadPath), testAlphaPayloadSource)

	gotReversed, err := collectFindings(base, []string{"pkg/zeta", "pkg/alpha"}, nil)
	if err != nil {
		t.Fatalf("collect findings reversed roots: %v", err)
	}

	gotCanonical, err := collectFindings(base, []string{"pkg/alpha", "pkg/zeta"}, nil)
	if err != nil {
		t.Fatalf("collect findings canonical roots: %v", err)
	}

	want := []violationSummary{
		{file: testAlphaPayloadPath, owner: testOwnerPayload, category: string(anyCategoryMapTypeKey), line: 2, column: 18},
		{file: testAlphaPayloadPath, owner: testOwnerPayload, category: string(anyCategoryMapTypeValue), line: 2, column: 22},
		{file: testZetaLaterPath, owner: testOwnerLater, category: string(anyCategoryCallExprFun), line: 2, column: 23},
		{file: testZetaLaterPath, owner: testOwnerLater, category: string(anyCategoryCallExprFun), line: 2, column: 31},
	}

	if got := collectFindingSummaries(gotReversed); !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected reversed-root finding order:\ngot: %#v\nwant: %#v", got, want)
	}
	if got := collectFindingSummaries(gotCanonical); !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected canonical-root finding order:\ngot: %#v\nwant: %#v", got, want)
	}
}

func TestSortViolationsOrdersByFileLineColumnCategoryAndOwner(t *testing.T) {
	violations := []Error{
		{
			File:   testZetaLaterPath,
			Line:   1,
			Column: 1,
			Identity: FindingIdentity{
				File:     testZetaLaterPath,
				Owner:    testOwnerLater,
				Category: string(anyCategoryCallExprFun),
			},
		},
		{
			File:   testAlphaPayloadPath,
			Line:   2,
			Column: 1,
			Identity: FindingIdentity{
				File:     testAlphaPayloadPath,
				Owner:    testOwnerPayload,
				Category: string(anyCategoryMapTypeValue),
			},
		},
		{
			File:   testAlphaPayloadPath,
			Line:   1,
			Column: 2,
			Identity: FindingIdentity{
				File:     testAlphaPayloadPath,
				Owner:    "Beta",
				Category: string(anyCategoryFieldType),
			},
		},
		{
			File:   testAlphaPayloadPath,
			Line:   1,
			Column: 1,
			Identity: FindingIdentity{
				File:     testAlphaPayloadPath,
				Owner:    "Zulu",
				Category: string(anyCategoryMapTypeValue),
			},
		},
		{
			File:   testAlphaPayloadPath,
			Line:   1,
			Column: 1,
			Identity: FindingIdentity{
				File:     testAlphaPayloadPath,
				Owner:    "Alpha",
				Category: string(anyCategoryMapTypeValue),
			},
		},
		{
			File:   testAlphaPayloadPath,
			Line:   1,
			Column: 1,
			Identity: FindingIdentity{
				File:     testAlphaPayloadPath,
				Owner:    testOwnerPayload,
				Category: string(anyCategoryMapTypeKey),
			},
		},
	}

	sortViolations(violations)

	got := collectViolationSummaries(violations)
	want := []violationSummary{
		{file: testAlphaPayloadPath, owner: testOwnerPayload, category: string(anyCategoryMapTypeKey), line: 1, column: 1},
		{file: testAlphaPayloadPath, owner: "Alpha", category: string(anyCategoryMapTypeValue), line: 1, column: 1},
		{file: testAlphaPayloadPath, owner: "Zulu", category: string(anyCategoryMapTypeValue), line: 1, column: 1},
		{file: testAlphaPayloadPath, owner: "Beta", category: string(anyCategoryFieldType), line: 1, column: 2},
		{file: testAlphaPayloadPath, owner: testOwnerPayload, category: string(anyCategoryMapTypeValue), line: 2, column: 1},
		{file: testZetaLaterPath, owner: testOwnerLater, category: string(anyCategoryCallExprFun), line: 1, column: 1},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected sorted violations:\ngot: %#v\nwant: %#v", got, want)
	}
}

func TestSortCollectedFindingsOrdersByFileLineColumnCategoryOwnerCodeAndSuppression(t *testing.T) {
	const (
		testCodeLater     = "later"
		testCodeValueLate = "value-late"
		testCodeBeta      = "beta"
		testCodeZulu      = "zulu"
		testCodeAlpha     = "alpha"
		testCodeKey       = "key"
		testCodeAAA       = "aaa"
		testCodeBBB       = "bbb"
	)

	findings := []collectedFinding{
		testCollectedFinding(testZetaLaterPath, testOwnerLater, anyCategoryCallExprFun, 1, 1, testCodeLater, false),
		testCollectedFinding(testAlphaPayloadPath, testOwnerPayload, anyCategoryMapTypeValue, 2, 1, testCodeValueLate, false),
		testCollectedFinding(testAlphaPayloadPath, testOwnerBeta, anyCategoryFieldType, 1, 2, testCodeBeta, false),
		testCollectedFinding(testAlphaPayloadPath, testOwnerZulu, anyCategoryMapTypeValue, 1, 1, testCodeZulu, false),
		testCollectedFinding(testAlphaPayloadPath, testOwnerAlpha, anyCategoryMapTypeValue, 1, 1, testCodeAlpha, false),
		testCollectedFinding(testAlphaPayloadPath, testOwnerPayload, anyCategoryMapTypeKey, 1, 1, testCodeKey, false),
		testCollectedFinding(testAlphaPayloadPath, testOwnerPayload, anyCategoryMapTypeValue, 1, 1, testCodeAAA, false),
		testCollectedFinding(testAlphaPayloadPath, testOwnerPayload, anyCategoryMapTypeValue, 1, 1, testCodeBBB, false),
		testCollectedFinding(testAlphaPayloadPath, testOwnerPayload, anyCategoryMapTypeValue, 1, 1, testCodeBBB, true),
	}

	sortCollectedFindings(findings)

	want := []collectedFinding{
		testCollectedFinding(testAlphaPayloadPath, testOwnerPayload, anyCategoryMapTypeKey, 1, 1, testCodeKey, false),
		testCollectedFinding(testAlphaPayloadPath, testOwnerAlpha, anyCategoryMapTypeValue, 1, 1, testCodeAlpha, false),
		testCollectedFinding(testAlphaPayloadPath, testOwnerPayload, anyCategoryMapTypeValue, 1, 1, testCodeAAA, false),
		testCollectedFinding(testAlphaPayloadPath, testOwnerPayload, anyCategoryMapTypeValue, 1, 1, testCodeBBB, false),
		testCollectedFinding(testAlphaPayloadPath, testOwnerPayload, anyCategoryMapTypeValue, 1, 1, testCodeBBB, true),
		testCollectedFinding(testAlphaPayloadPath, testOwnerZulu, anyCategoryMapTypeValue, 1, 1, testCodeZulu, false),
		testCollectedFinding(testAlphaPayloadPath, testOwnerBeta, anyCategoryFieldType, 1, 2, testCodeBeta, false),
		testCollectedFinding(testAlphaPayloadPath, testOwnerPayload, anyCategoryMapTypeValue, 2, 1, testCodeValueLate, false),
		testCollectedFinding(testZetaLaterPath, testOwnerLater, anyCategoryCallExprFun, 1, 1, testCodeLater, false),
	}

	if !reflect.DeepEqual(findings, want) {
		t.Fatalf("unexpected sorted findings:\ngot: %#v\nwant: %#v", findings, want)
	}
}

func TestAnyAllowlistIndexMatchesStrictSelectorIdentity(t *testing.T) {
	index := buildAllowlistIndex(AnyAllowlist{
		Version: anyAllowlistVersion,
		Entries: []AnyAllowlistEntry{
			allowlistEntry(testPayloadPath, testOwnerUse, anyCategoryCallExprFun, "exact selector allow"),
		},
	})

	if !index.isAllowed(newFindingIdentity(testPayloadPath, testOwnerUse, anyCategoryCallExprFun)) {
		t.Fatalf("expected exact selector match")
	}
	if index.isAllowed(newFindingIdentity(testPayloadPath, testOwnerUse, anyCategoryFieldType)) {
		t.Fatalf("did not expect selector to match a different category")
	}
	if index.isAllowed(newFindingIdentity(testPayloadPath, testOwnerPayload, anyCategoryCallExprFun)) {
		t.Fatalf("did not expect selector to match a different owner")
	}
}

func TestValidateAnyUsageUsesEnclosingFunctionOwnerForLocalDeclarations(t *testing.T) {
	base := t.TempDir()
	writeFile(t, apiPath(base, testPayloadFile), "package api\nfunc Use() {\n\ttype Hidden = any\n\tvar local map[string]any\n\t_ = func(v any) {}\n}\n")

	allowlist := AnyAllowlist{
		Version: anyAllowlistVersion,
		Entries: []AnyAllowlistEntry{
			allowlistEntry(testPayloadPath, testOwnerUse, anyCategoryTypeSpecType, "allow local type alias"),
			allowlistEntry(testPayloadPath, testOwnerUse, anyCategoryMapTypeValue, "allow local map usage"),
			allowlistEntry(testPayloadPath, testOwnerUse, anyCategoryFieldType, "allow nested function parameter"),
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

func TestCollectParsedPackagesGroupsByDirectoryAndPackage(t *testing.T) {
	base := t.TempDir()
	testAPath := normalizePath(filepath.Join(testRootAPI, "a.go"))
	testZPath := normalizePath(filepath.Join(testRootAPI, "z.go"))
	testExternalTestPath := normalizePath(filepath.Join(testRootAPI, "external_test.go"))

	writeFile(t, filepath.Join(base, testAPath), testPackageAPISource)
	writeFile(t, filepath.Join(base, testZPath), testPackageAPISource)
	writeFile(t, filepath.Join(base, testExternalTestPath), "package api_test\n")

	parsed, err := collectParsedPackages(filepath.Join(base, "pkg"), base, nil)
	if err != nil {
		t.Fatalf("collect parsed packages: %v", err)
	}
	if parsed.fset == nil {
		t.Fatalf("expected parsed package collection to retain a file set")
	}
	packages := parsed.packages

	if len(packages) != 2 {
		t.Fatalf("expected two grouped packages, got %d", len(packages))
	}

	if packages[0].dir != testRootAPI || packages[0].name != testDirAPI {
		t.Fatalf("unexpected first package: dir=%q name=%q", packages[0].dir, packages[0].name)
	}
	if got := []string{packages[0].files[0].relPath, packages[0].files[1].relPath}; !reflect.DeepEqual(got, []string{testAPath, testZPath}) {
		t.Fatalf("unexpected api package files: got %#v", got)
	}

	if packages[1].dir != testRootAPI || packages[1].name != "api_test" {
		t.Fatalf("unexpected second package: dir=%q name=%q", packages[1].dir, packages[1].name)
	}
	if len(packages[1].files) != 1 || packages[1].files[0].relPath != testExternalTestPath {
		t.Fatalf("unexpected api_test package files: %#v", packages[1].files)
	}
}

func TestParseRootFileSkipsDirectory(t *testing.T) {
	base := t.TempDir()
	fset := token.NewFileSet()
	entry := dirEntryFromPath(t, base)

	_, keep, err := parseRootFile(fset, base, entry, nil, base, nil)
	if err != nil {
		t.Fatalf("parse directory: %v", err)
	}
	if keep {
		t.Fatalf("expected directory to be skipped")
	}
}

func TestParseRootFileSkipsExcludedFile(t *testing.T) {
	base := t.TempDir()
	fset := token.NewFileSet()
	path := filepath.Join(base, testRootAPI, testPayloadTestFile)
	writeFile(t, path, testPackageAPISource)
	entry := dirEntryFromPath(t, path)

	_, keep, err := parseRootFile(fset, path, entry, nil, base, []string{"**/*_test.go"})
	if err != nil {
		t.Fatalf("parse excluded file: %v", err)
	}
	if keep {
		t.Fatalf("expected excluded file to be skipped")
	}
}

func TestParseRootFileReportsParseErrors(t *testing.T) {
	base := t.TempDir()
	fset := token.NewFileSet()
	path := filepath.Join(base, testRootAPI, testBrokenFile)
	writeFile(t, path, testBrokenGoSource)
	entry := dirEntryFromPath(t, path)

	_, keep, err := parseRootFile(fset, path, entry, nil, base, nil)
	if err == nil {
		t.Fatal(testExpectedParseError)
	}
	if keep {
		t.Fatalf("did not expect broken file to be kept")
	}
}

func TestParseRootFileReturnsParsedGoFile(t *testing.T) {
	base := t.TempDir()
	fset := token.NewFileSet()
	path := filepath.Join(base, testPayloadPath)
	writeFile(t, path, testPayloadSource)
	entry := dirEntryFromPath(t, path)

	parsed, keep, err := parseRootFile(fset, path, entry, nil, base, nil)
	if err != nil {
		t.Fatalf(testParseFileErrFmt, err)
	}
	if !keep {
		t.Fatalf("expected go file to be kept")
	}
	if parsed.relPath != testPayloadPath {
		t.Fatalf("unexpected rel path: %q", parsed.relPath)
	}
	if parsed.syntax.Name.Name != testDirAPI {
		t.Fatalf("unexpected package name: %q", parsed.syntax.Name.Name)
	}
}

func TestTypeCheckParsedPackageKeepsPartialInfoOnErrors(t *testing.T) {
	src := `package api

func Use(value any) {
	_ = Missing
	_ = any(value)
}
`

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, testSamplePath, src, parser.ParseComments)
	if err != nil {
		t.Fatalf(testParseFileErrFmt, err)
	}

	info := typeCheckParsedPackage(fset, importer.ForCompiler(fset, sourceImporterMode, nil), parsedPackage{
		dir:  testRootAPI,
		name: testDirAPI,
		files: []parsedGoFile{
			{
				relPath: testSamplePath,
				content: []byte(src),
				syntax:  file,
			},
		},
	})

	got := collectAnyUsages(testSamplePath, file, info)
	want := []usageSummary{
		{category: anyCategoryFieldType, owner: "Use", line: 3},
		{category: anyCategoryCallExprFun, owner: "Use", line: 5},
	}
	if summaries := summarizeUsages(fset, got); !reflect.DeepEqual(summaries, want) {
		t.Fatalf("unexpected partial-type-info usages:\ngot: %#v\nwant: %#v", summaries, want)
	}
}

func TestNormalizePathAndLineCodeHelpers(t *testing.T) {
	if got := normalizePath(" ./pkg/api/../api/payload.go "); got != testPayloadPath {
		t.Fatalf("unexpected normalized path: %q", got)
	}
	if got := lineCode(0, []string{"first"}); got != "" {
		t.Fatalf("expected empty code for line 0, got %q", got)
	}
	if got := lineCode(2, []string{"first", " second "}); got != "second" {
		t.Fatalf("unexpected line code: %q", got)
	}
	if got := lineCode(3, []string{"first"}); got != "" {
		t.Fatalf("expected empty code for out-of-range line, got %q", got)
	}
}

func TestValueSpecOwnerPrefersFirstDeclaredName(t *testing.T) {
	src := `package p

var First, Second int
`

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, testSamplePath, src, parser.ParseComments)
	if err != nil {
		t.Fatalf(testParseFileErrFmt, err)
	}

	valueDecl, ok := file.Decls[0].(*ast.GenDecl)
	if !ok || len(valueDecl.Specs) == 0 {
		t.Fatalf("expected leading value declaration")
	}
	valueSpec, ok := valueDecl.Specs[0].(*ast.ValueSpec)
	if !ok {
		t.Fatalf("expected leading value spec")
	}

	if got := valueSpecOwner(valueSpec); got != "First" {
		t.Fatalf("unexpected value spec owner: %q", got)
	}
	if got := valueSpecOwner(&ast.ValueSpec{Names: []*ast.Ident{nil}}); got != "" {
		t.Fatalf("expected empty owner for nil name, got %q", got)
	}
}

func TestFuncDeclOwnerUsesReceiverTypeName(t *testing.T) {
	src := `package p

func Plain() {}

type Receiver[T any] struct{}
type Pair[T, U any] struct{}

func (r *Receiver[int]) Method() {}
func (p Pair[int, string]) Multi() {}
`

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, testSamplePath, src, parser.ParseComments)
	if err != nil {
		t.Fatalf(testParseFileErrFmt, err)
	}

	plainDecl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected plain function declaration")
	}
	methodDecl, ok := file.Decls[3].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected method declaration")
	}
	multiDecl, ok := file.Decls[4].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected indexed receiver method declaration")
	}
	if got := funcDeclOwner(plainDecl); got != "Plain" {
		t.Fatalf("unexpected plain function owner: %q", got)
	}
	if got := funcDeclOwner(methodDecl); got != "Receiver" {
		t.Fatalf("unexpected method owner: %q", got)
	}
	if got := funcDeclOwner(multiDecl); got != "Pair" {
		t.Fatalf("unexpected indexed receiver owner: %q", got)
	}
	if got := receiverTypeName(&ast.SelectorExpr{}); got != "" {
		t.Fatalf("expected unsupported receiver expression to stay empty, got %q", got)
	}
}

type usageSummary struct {
	category anyUsageCategory
	owner    string
	line     int
}

type violationSummary struct {
	file     string
	owner    string
	category string
	line     int
	column   int
}

func collectUsageSummaries(t *testing.T, src string) []usageSummary {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "sample.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf(testParseFileErrFmt, err)
	}

	info := typeCheckTestFile(fset, file)
	return summarizeUsages(fset, collectAnyUsages(testSamplePath, file, info))
}

func typeCheckTestFile(fset *token.FileSet, file *ast.File) *types.Info {
	info := &types.Info{
		Uses: make(map[*ast.Ident]types.Object),
	}
	config := types.Config{
		DisableUnusedImportCheck: true,
		Error:                    func(error) {},
		Importer:                 importer.ForCompiler(fset, sourceImporterMode, nil),
	}
	// Focused snippets may omit unrelated declarations; partial type info is enough here.
	if _, err := config.Check("sample", fset, []*ast.File{file}, info); err != nil {
		return info
	}
	return info
}

func summarizeUsages(fset *token.FileSet, usages []anyUsage) []usageSummary {
	summaries := make([]usageSummary, 0, len(usages))
	for _, usage := range usages {
		summaries = append(summaries, usageSummary{
			category: anyUsageCategory(usage.identity.Category),
			owner:    usage.identity.Owner,
			line:     fset.Position(usage.pos).Line,
		})
	}
	return summaries
}

func findAnyIdentOnLine(t *testing.T, fset *token.FileSet, file *ast.File, line int) *ast.Ident {
	t.Helper()

	var ident *ast.Ident
	ast.Inspect(file, func(node ast.Node) bool {
		candidate, ok := node.(*ast.Ident)
		if !ok || candidate.Name != anyName {
			return true
		}
		if fset.Position(candidate.Pos()).Line != line {
			return true
		}
		if ident != nil {
			t.Fatalf("expected one any identifier on line %d", line)
		}
		ident = candidate
		return true
	})
	if ident == nil {
		t.Fatalf("expected any identifier on line %d", line)
	}
	return ident
}

func dirEntryFromPath(t *testing.T, path string) fs.DirEntry {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return fs.FileInfoToDirEntry(info)
}

func collectViolationSummaries(violations []Error) []violationSummary {
	summaries := make([]violationSummary, 0, len(violations))
	for _, violation := range violations {
		summaries = append(summaries, violationSummary{
			file:     violation.Identity.File,
			owner:    violation.Identity.Owner,
			category: violation.Identity.Category,
			line:     violation.Line,
			column:   violation.Column,
		})
	}
	return summaries
}

func collectFindingSummaries(findings []collectedFinding) []violationSummary {
	summaries := make([]violationSummary, 0, len(findings))
	for _, finding := range findings {
		summaries = append(summaries, violationSummary{
			file:     finding.identity.File,
			owner:    finding.identity.Owner,
			category: finding.identity.Category,
			line:     finding.line,
			column:   finding.column,
		})
	}
	return summaries
}

func testCollectedFinding(
	path, owner string,
	category anyUsageCategory,
	line, column int,
	code string,
	suppressed bool,
) collectedFinding {
	return collectedFinding{
		identity:           newFindingIdentity(path, owner, category),
		line:               line,
		column:             column,
		code:               code,
		suppressedByNolint: suppressed,
	}
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

func allowlistEntry(path, owner string, category anyUsageCategory, description string) AnyAllowlistEntry {
	return AnyAllowlistEntry{
		Selector: &AnyAllowlistSelector{
			Path:     path,
			Owner:    owner,
			Category: string(category),
		},
		Description: description,
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
