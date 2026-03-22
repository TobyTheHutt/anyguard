package main

import (
	"testing"

	"github.com/tobythehutt/anyguard/v2/internal/validation"
	"golang.org/x/tools/go/analysis"
)

func TestMainWiresAnalyzer(t *testing.T) {
	original := runSinglechecker
	t.Cleanup(func() { runSinglechecker = original })

	called := false
	runSinglechecker = func(analyzer *analysis.Analyzer) {
		called = true
		if analyzer == nil {
			t.Fatalf("expected analyzer instance")
		}
		if analyzer.Name != validation.AnalyzerName {
			t.Fatalf("unexpected analyzer name: %q", analyzer.Name)
		}
	}

	main()

	if !called {
		t.Fatalf("expected runSinglechecker to be called")
	}
}
