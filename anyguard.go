// Package anyguard exposes the public go/analysis entrypoint.
package anyguard

import (
	"github.com/tobythehutt/anyguard/v2/internal/validation"
	"golang.org/x/tools/go/analysis"
)

const (
	// AnalyzerName is the stable linter/analyzer name.
	AnalyzerName = validation.AnalyzerName
	// DefaultAllowlistPath is the default YAML allowlist file location.
	DefaultAllowlistPath = validation.DefaultAllowlistPath
	// DefaultRoots defines the default configured roots to analyze.
	DefaultRoots = validation.DefaultRoots
)

// NewAnalyzer constructs a go/analysis analyzer for any-usage validation.
func NewAnalyzer() *analysis.Analyzer {
	return validation.NewAnalyzer()
}
