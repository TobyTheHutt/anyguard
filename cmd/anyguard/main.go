// Command anyguard runs the anyguard go/analysis analyzer.
package main

import (
	"github.com/tobythehutt/anyguard/v2/internal/validation"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/singlechecker"
)

var runSinglechecker = func(analyzer *analysis.Analyzer) {
	singlechecker.Main(analyzer)
}

func main() {
	runSinglechecker(validation.NewAnalyzer())
}
