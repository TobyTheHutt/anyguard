package anyguard

import "testing"

func TestNewAnalyzer(t *testing.T) {
	analyzer := NewAnalyzer()
	if analyzer == nil {
		t.Fatal("expected analyzer")
	}

	if got, want := analyzer.Name, AnalyzerName; got != want {
		t.Fatalf("analyzer name = %q, want %q", got, want)
	}
}
