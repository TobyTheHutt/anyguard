package validation

import (
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const (
	corpusFixtureSupported          = "supported"
	corpusFixtureBoundary           = "boundary"
	corpusFixtureDeclarationSlots   = "declaration-slots"
	corpusFixtureCompositeSlots     = "composite-slots"
	corpusFixtureUnsupported        = "unsupported"
	corpusFixtureAllowlistHygiene   = "allowlist-hygiene"
	corpusFixtureStabilityBase      = "stability/base"
	corpusFixtureStabilityCanonical = "stability/canonical"
)

func TestValidateAnyUsageCorpusSupportedMatrix(t *testing.T) {
	t.Run("disallowed", func(t *testing.T) {
		got := collectViolationSummaries(mustValidateCorpus(t, corpusFixtureSupported, testAllowlistEmpty, []string{DefaultRoots}))
		want := []violationSummary{
			{file: "pkg/api/supported.go", owner: "FieldTypeDisallowed", category: string(anyCategoryFieldType), line: 3, column: 28},
			{file: "pkg/api/supported.go", owner: "ValueSpecDisallowed", category: string(anyCategoryValueSpecType), line: 5, column: 25},
			{file: "pkg/api/supported.go", owner: "TypeSpecDisallowed", category: string(anyCategoryTypeSpecType), line: 7, column: 27},
			{file: "pkg/api/supported.go", owner: "ArrayTypeDisallowed", category: string(anyCategoryArrayTypeElt), line: 9, column: 28},
			{file: "pkg/api/supported.go", owner: "MapKeyDisallowed", category: string(anyCategoryMapTypeKey), line: 11, column: 27},
			{file: "pkg/api/supported.go", owner: "MapValueDisallowed", category: string(anyCategoryMapTypeValue), line: 13, column: 36},
			{file: "pkg/api/supported.go", owner: "ChanTypeDisallowed", category: string(anyCategoryChanTypeValue), line: 15, column: 33},
			{file: "pkg/api/supported.go", owner: "StarTypeDisallowed", category: string(anyCategoryStarExprX), line: 17, column: 26},
			{file: "pkg/api/supported.go", owner: "EllipsisDisallowed", category: string(anyCategoryEllipsisElt), line: 19, column: 35},
			{file: "pkg/api/supported.go", owner: "TypeAssertDisallowed", category: string(anyCategoryTypeAssertType), line: 22, column: 13},
			{file: "pkg/api/supported.go", owner: "CallExprDisallowed", category: string(anyCategoryCallExprFun), line: 26, column: 6},
			{file: "pkg/api/supported.go", owner: "IndexExprDisallowed", category: string(anyCategoryIndexExprIndex), line: 34, column: 13},
			{file: "pkg/api/supported.go", owner: "IndexListDisallowed", category: string(anyCategoryIndexListIndex), line: 38, column: 15},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("unexpected supported corpus violations:\ngot: %#v\nwant: %#v", got, want)
		}
	})

	t.Run("allowed", func(t *testing.T) {
		violations := mustValidateCorpus(t, corpusFixtureSupported, "allowlist-all.yaml", []string{DefaultRoots})
		if len(violations) != 0 {
			t.Fatalf("expected supported allowlist corpus to be clean, got %v", violations)
		}
	})

	t.Run(corpusFixtureBoundary, func(t *testing.T) {
		got := collectViolationSummaries(mustValidateCorpus(t, corpusFixtureBoundary, testAllowlistEmpty, []string{DefaultRoots}))
		want := []violationSummary{
			{file: "pkg/api/boundary.go", owner: "NestedArray", category: string(anyCategoryArrayTypeElt), line: 3, column: 31},
			{file: "pkg/api/boundary.go", owner: "NestedMap", category: string(anyCategoryMapTypeValue), line: 5, column: 29},
			{file: "pkg/api/boundary.go", owner: "Boundary", category: string(anyCategoryArrayTypeElt), line: 7, column: 27},
			{file: "pkg/api/boundary.go", owner: "Boundary", category: string(anyCategoryArrayTypeElt), line: 7, column: 45},
			{file: "pkg/api/boundary.go", owner: "Boundary", category: string(anyCategoryMapTypeKey), line: 8, column: 18},
			{file: "pkg/api/boundary.go", owner: "Boundary", category: string(anyCategoryArrayTypeElt), line: 9, column: 28},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("unexpected boundary corpus violations:\ngot: %#v\nwant: %#v", got, want)
		}
	})
}

func TestValidateAnyUsageCorpusUnsupportedContexts(t *testing.T) {
	got := collectViolationSummaries(mustValidateCorpus(t, corpusFixtureUnsupported, testAllowlistEmpty, []string{DefaultRoots}))
	if len(got) != 0 {
		t.Fatalf("expected unsupported corpus to remain unreported, got %#v", got)
	}
}

func TestValidateAnyUsageCorpusDeclarationSlots(t *testing.T) {
	got := collectViolationSummaries(mustValidateCorpus(t, corpusFixtureDeclarationSlots, testAllowlistEmpty, []string{DefaultRoots}))
	want := []violationSummary{
		{
			file:     "pkg/predeclared/declarations.go",
			owner:    "FieldTypePredeclared",
			category: string(anyCategoryFieldType),
			line:     3,
			column:   33,
		},
		{
			file:     "pkg/predeclared/declarations.go",
			owner:    "ValueSpecPredeclared",
			category: string(anyCategoryValueSpecType),
			line:     5,
			column:   26,
		},
		{
			file:     "pkg/predeclared/declarations.go",
			owner:    "TypeSpecPredeclared",
			category: string(anyCategoryTypeSpecType),
			line:     7,
			column:   28,
		},
		{
			file:     "pkg/predeclared/declarations.go",
			owner:    "TypeAssertPredeclared",
			category: string(anyCategoryTypeAssertType),
			line:     10,
			column:   13,
		},
		{
			file:     "pkg/predeclared/local.go",
			owner:    "LocalPredeclared",
			category: string(anyCategoryValueSpecType),
			line:     4,
			column:   12,
		},
		{
			file:     "pkg/predeclared/local.go",
			owner:    "LocalPredeclared",
			category: string(anyCategoryTypeSpecType),
			line:     5,
			column:   20,
		},
		{
			file:     "pkg/predeclared/local.go",
			owner:    "LocalPredeclared",
			category: string(anyCategoryTypeAssertType),
			line:     7,
			column:   13,
		},
		{
			file:     "pkg/predeclared/local.go",
			owner:    "LocalPredeclared",
			category: string(anyCategoryFieldType),
			line:     8,
			column:   16,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected declaration-slot corpus violations:\ngot: %#v\nwant: %#v", got, want)
	}
}

func TestValidateAnyUsageCorpusCompositeSlots(t *testing.T) {
	got := collectViolationSummaries(mustValidateCorpus(t, corpusFixtureCompositeSlots, testAllowlistEmpty, []string{DefaultRoots}))
	want := []violationSummary{
		{
			file:     "pkg/predeclared/composites.go",
			owner:    "ArrayAlias",
			category: string(anyCategoryArrayTypeElt),
			line:     3,
			column:   21,
		},
		{
			file:     "pkg/predeclared/composites.go",
			owner:    "MapKeyAlias",
			category: string(anyCategoryMapTypeKey),
			line:     4,
			column:   24,
		},
		{
			file:     "pkg/predeclared/composites.go",
			owner:    "MapValueAlias",
			category: string(anyCategoryMapTypeValue),
			line:     5,
			column:   33,
		},
		{
			file:     "pkg/predeclared/composites.go",
			owner:    "ChanAlias",
			category: string(anyCategoryChanTypeValue),
			line:     6,
			column:   23,
		},
		{
			file:     "pkg/predeclared/composites.go",
			owner:    "StarAlias",
			category: string(anyCategoryStarExprX),
			line:     7,
			column:   19,
		},
		{
			file:     "pkg/predeclared/composites.go",
			owner:    "NestedArrayAlias",
			category: string(anyCategoryArrayTypeElt),
			line:     9,
			column:   38,
		},
		{
			file:     "pkg/predeclared/composites.go",
			owner:    "NestedMapAlias",
			category: string(anyCategoryMapTypeValue),
			line:     10,
			column:   36,
		},
		{
			file:     "pkg/predeclared/composites.go",
			owner:    "EllipsisAlias",
			category: string(anyCategoryEllipsisElt),
			line:     12,
			column:   30,
		},
		{
			file:     "pkg/predeclared/composites.go",
			owner:    "NestedEllipsisAlias",
			category: string(anyCategoryArrayTypeElt),
			line:     13,
			column:   38,
		},
		{
			file:     "pkg/predeclared/local.go",
			owner:    "LocalPredeclaredComposites",
			category: string(anyCategoryArrayTypeElt),
			line:     4,
			column:   14,
		},
		{
			file:     "pkg/predeclared/local.go",
			owner:    "LocalPredeclaredComposites",
			category: string(anyCategoryMapTypeKey),
			line:     5,
			column:   16,
		},
		{
			file:     "pkg/predeclared/local.go",
			owner:    "LocalPredeclaredComposites",
			category: string(anyCategoryMapTypeValue),
			line:     6,
			column:   24,
		},
		{
			file:     "pkg/predeclared/local.go",
			owner:    "LocalPredeclaredComposites",
			category: string(anyCategoryChanTypeValue),
			line:     7,
			column:   18,
		},
		{
			file:     "pkg/predeclared/local.go",
			owner:    "LocalPredeclaredComposites",
			category: string(anyCategoryStarExprX),
			line:     8,
			column:   11,
		},
		{
			file:     "pkg/predeclared/local.go",
			owner:    "LocalPredeclaredComposites",
			category: string(anyCategoryEllipsisElt),
			line:     10,
			column:   21,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected composite-slot corpus violations:\ngot: %#v\nwant: %#v", got, want)
	}
}

func TestValidateAnyUsageCorpusStability(t *testing.T) {
	base := collectViolationSummaries(mustValidateCorpus(t, corpusFixtureStabilityBase, testAllowlistEmpty, []string{"pkg/alpha", "pkg/zeta"}))
	want := []violationSummary{
		{file: "pkg/alpha/payload.go", owner: "Payload", category: string(anyCategoryMapTypeKey), line: 3, column: 18},
		{file: "pkg/alpha/payload.go", owner: "Payload", category: string(anyCategoryMapTypeValue), line: 3, column: 22},
		{file: "pkg/zeta/later.go", owner: "Later", category: string(anyCategoryCallExprFun), line: 3, column: 23},
		{file: "pkg/zeta/later.go", owner: "Later", category: string(anyCategoryCallExprFun), line: 3, column: 31},
	}
	if !reflect.DeepEqual(base, want) {
		t.Fatalf("unexpected base stability corpus violations:\ngot: %#v\nwant: %#v", base, want)
	}

	t.Run("file order changes", func(t *testing.T) {
		reversed := collectViolationSummaries(mustValidateCorpus(t, corpusFixtureStabilityBase, testAllowlistEmpty, []string{"pkg/zeta", "pkg/alpha"}))
		if !reflect.DeepEqual(reversed, base) {
			t.Fatalf("expected identical results across root order changes:\ngot: %#v\nwant: %#v", reversed, base)
		}
	})

	t.Run("declaration order changes", func(t *testing.T) {
		got := collectIdentitySummaries(mustValidateCorpus(t, "stability/declaration-order", testAllowlistEmpty, []string{DefaultRoots}))
		wantIdentities := collectIdentitySummaries(mustValidateCorpus(t, corpusFixtureStabilityCanonical, testAllowlistEmpty, []string{DefaultRoots}))
		if !reflect.DeepEqual(got, wantIdentities) {
			t.Fatalf("expected identical finding identities across declaration order changes:\ngot: %#v\nwant: %#v", got, wantIdentities)
		}
	})

	t.Run("irrelevant comments", func(t *testing.T) {
		got := collectViolationSummaries(mustValidateCorpus(t, "stability/comments", testAllowlistEmpty, []string{DefaultRoots}))
		wantCanonical := collectViolationSummaries(mustValidateCorpus(t, corpusFixtureStabilityCanonical, testAllowlistEmpty, []string{DefaultRoots}))
		if !reflect.DeepEqual(got, wantCanonical) {
			t.Fatalf("expected identical results across comment noise:\ngot: %#v\nwant: %#v", got, wantCanonical)
		}
	})

	t.Run("formatting noise", func(t *testing.T) {
		got := collectViolationSummaries(mustValidateCorpus(t, "stability/formatting", testAllowlistEmpty, []string{DefaultRoots}))
		wantCanonical := collectViolationSummaries(mustValidateCorpus(t, corpusFixtureStabilityCanonical, testAllowlistEmpty, []string{DefaultRoots}))
		if !reflect.DeepEqual(got, wantCanonical) {
			t.Fatalf("expected identical results across formatting noise:\ngot: %#v\nwant: %#v", got, wantCanonical)
		}
	})
}

func TestValidateAnyUsageCorpusAllowlistHygiene(t *testing.T) {
	testCases := []struct {
		name        string
		allowlist   string
		wantMessage string
	}{
		{
			name:        "stale selector",
			allowlist:   "allowlist-stale.yaml",
			wantMessage: "does not match any finding",
		},
		{
			name:        "typoed category",
			allowlist:   "allowlist-typo-category.yaml",
			wantMessage: "unknown category",
		},
		{
			name:        "typoed symbol",
			allowlist:   "allowlist-typo-owner.yaml",
			wantMessage: "does not match any finding",
		},
		{
			name:        "malformed selector",
			allowlist:   "allowlist-malformed.yaml",
			wantMessage: "selector missing owner",
		},
		{
			name:        "unresolved selector",
			allowlist:   "allowlist-unresolved.yaml",
			wantMessage: "does not match any finding",
		},
		{
			name:        "overbroad allow",
			allowlist:   "allowlist-overbroad.yaml",
			wantMessage: "legacy allowlist entry shape is unsupported",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := ValidateAnyUsageFromFile(
				filepath.Join(corpusFixtureDir(t, corpusFixtureAllowlistHygiene), testCase.allowlist),
				corpusFixtureDir(t, corpusFixtureAllowlistHygiene),
				[]string{DefaultRoots},
			)
			if err == nil {
				t.Fatalf("expected config error for %s", testCase.allowlist)
			}
			if !strings.Contains(err.Error(), testCase.wantMessage) {
				t.Fatalf("unexpected config error: %v", err)
			}
		})
	}
}

type identitySummary struct {
	file     string
	owner    string
	category string
}

func mustValidateCorpus(t *testing.T, fixture, allowlist string, roots []string) []Error {
	t.Helper()

	base := corpusFixtureDir(t, fixture)
	violations, err := ValidateAnyUsageFromFile(filepath.Join(base, allowlist), base, roots)
	if err != nil {
		t.Fatalf("validate corpus %s: %v", fixture, err)
	}
	return violations
}

func corpusFixtureDir(t *testing.T, fixture string) string {
	t.Helper()
	return filepath.Join("testdata", "corpus", fixture)
}

func collectIdentitySummaries(violations []Error) []identitySummary {
	summaries := make([]identitySummary, 0, len(violations))
	for _, violation := range violations {
		summaries = append(summaries, identitySummary{
			file:     violation.Identity.File,
			owner:    violation.Identity.Owner,
			category: violation.Identity.Category,
		})
	}
	sort.Slice(summaries, func(i, j int) bool {
		left := summaries[i]
		right := summaries[j]
		switch {
		case left.file != right.file:
			return left.file < right.file
		case left.owner != right.owner:
			return left.owner < right.owner
		default:
			return left.category < right.category
		}
	})
	return summaries
}
