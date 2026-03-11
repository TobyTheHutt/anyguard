package validation

import (
	"errors"
	"fmt"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"golang.org/x/tools/go/analysis"
)

const (
	// AnalyzerName is the exposed go/analysis analyzer name.
	AnalyzerName = "anyguard"
	// DefaultAllowlistPath is the default YAML allowlist file location.
	DefaultAllowlistPath = "internal/ci/any_allowlist.yaml"
	// DefaultRoots defines the default configured roots to analyze.
	DefaultRoots = "./..."

	flagAllowlist = "allowlist"
	flagRoots     = "roots"
	flagRepoRoot  = "repo-root"

	errNoRootsProvided = "no roots provided for any usage validation"
	goPathSrcSegment   = "/src/"
)

// NewAnalyzer constructs a go/analysis analyzer for any-usage validation.
func NewAnalyzer() *analysis.Analyzer {
	cfg := &analyzerConfig{
		allowlistPath: DefaultAllowlistPath,
		roots:         DefaultRoots,
	}
	analyzer := &analysis.Analyzer{
		Name:       AnalyzerName,
		Doc:        "reports disallowed usage of the Go any type",
		Run:        cfg.run,
		ResultType: reflect.TypeOf(analysisResult{}),
	}
	analyzer.Flags.StringVar(&cfg.allowlistPath, flagAllowlist, DefaultAllowlistPath, "path to any usage allowlist YAML")
	analyzer.Flags.StringVar(&cfg.roots, flagRoots, DefaultRoots, "comma-separated roots to scan")
	analyzer.Flags.StringVar(&cfg.repoRoot, flagRepoRoot, "", "repository root (auto-detected when empty)")
	return analyzer
}

type analyzerConfig struct {
	allowlistPath string
	roots         string
	repoRoot      string
}

type analyzerFile struct {
	absPath   string
	relPath   string
	tokenFile *token.File
}

type analysisResult struct{}

func (cfg *analyzerConfig) run(pass *analysis.Pass) (any, error) {
	roots := splitRoots(cfg.roots)
	if len(roots) == 0 {
		return nil, errors.New(errNoRootsProvided)
	}

	repoRoot, err := cfg.resolveRepoRoot(pass)
	if err != nil {
		return nil, err
	}

	allowlistPath, err := resolveAllowlistPath(repoRoot, cfg.allowlistPath)
	if err != nil {
		return nil, err
	}
	allowlist, err := LoadAnyAllowlist(allowlistPath)
	if err != nil {
		return nil, err
	}
	index := buildAllowlistIndex(allowlist)

	files, err := collectAnalyzerFiles(pass, repoRoot, roots)
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		if shouldExclude(file.relPath, allowlist.ExcludeGlobs) || index.allowAll[file.relPath] {
			continue
		}

		violations, validateErr := validateAnyFile(file.absPath, file.relPath, index)
		if validateErr != nil {
			return nil, fmt.Errorf("validate %s: %w", file.relPath, validateErr)
		}
		reportViolations(pass, file.tokenFile, violations)
	}
	return analysisResult{}, nil
}

func (cfg *analyzerConfig) resolveRepoRoot(pass *analysis.Pass) (string, error) {
	if cfg.repoRoot != "" {
		repoRoot, err := filepath.Abs(cfg.repoRoot)
		if err != nil {
			return "", fmt.Errorf("resolve repo-root: %w", err)
		}
		return repoRoot, nil
	}

	filename := firstPassFilename(pass)
	if filename != "" {
		if root, ok := findGoModRoot(filepath.Dir(filename)); ok {
			return root, nil
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}
	if root, ok := findGoModRoot(cwd); ok {
		return root, nil
	}
	return cwd, nil
}

func firstPassFilename(pass *analysis.Pass) string {
	if len(pass.Files) == 0 {
		return ""
	}
	return pass.Fset.PositionFor(pass.Files[0].Package, false).Filename
}

func findGoModRoot(start string) (string, bool) {
	dir := filepath.Clean(start)
	for {
		if fileExists(filepath.Join(dir, "go.mod")) {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func resolveAllowlistPath(repoRoot, configured string) (string, error) {
	if filepath.IsAbs(configured) {
		return filepath.Clean(configured), nil
	}
	path := filepath.Join(repoRoot, configured)
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve allowlist path: %w", err)
	}
	return abs, nil
}

func collectAnalyzerFiles(pass *analysis.Pass, repoRoot string, roots []string) ([]analyzerFile, error) {
	filteredRoots := normalizeConfiguredRoots(roots, repoRoot)
	if len(filteredRoots) == 0 {
		return nil, errors.New("no usable roots after normalization")
	}

	pkgPath := ""
	if pass.Pkg != nil {
		pkgPath = pass.Pkg.Path()
	}

	files := make([]analyzerFile, 0, len(pass.Files))
	for _, file := range pass.Files {
		pos := pass.Fset.PositionFor(file.Package, false)
		if pos.Filename == "" {
			continue
		}

		relPath, err := relativePath(repoRoot, pos.Filename, pkgPath)
		if err != nil {
			return nil, fmt.Errorf("compute relative path for %s: %w", pos.Filename, err)
		}
		if !isWithinRoots(relPath, filteredRoots) {
			continue
		}

		tokenFile := pass.Fset.File(file.Package)
		if tokenFile == nil {
			continue
		}
		files = append(files, analyzerFile{
			absPath:   filepath.Clean(pos.Filename),
			relPath:   relPath,
			tokenFile: tokenFile,
		})
	}
	return files, nil
}

func relativePath(repoRoot, absPath, pkgPath string) (string, error) {
	relPath, err := filepath.Rel(repoRoot, absPath)
	if err == nil {
		normalized := normalizePath(relPath)
		if !isEscapingPath(normalized) {
			return normalized, nil
		}
	}

	if gopathRel, ok := pathFromGoPathSrc(absPath); ok {
		return gopathRel, nil
	}
	if pkgPath == "" {
		return "", errors.New("cannot resolve relative file path")
	}
	return normalizePath(filepath.Join(pkgPath, filepath.Base(absPath))), nil
}

func isEscapingPath(path string) bool {
	return path == ".." || strings.HasPrefix(path, "../")
}

func pathFromGoPathSrc(absPath string) (string, bool) {
	slash := filepath.ToSlash(absPath)
	idx := strings.Index(slash, goPathSrcSegment)
	if idx == -1 {
		return "", false
	}
	return normalizePath(slash[idx+len(goPathSrcSegment):]), true
}

func normalizeConfiguredRoots(roots []string, repoRoot string) []string {
	normalized := make([]string, 0, len(roots))
	for _, root := range roots {
		root = normalizeRootValue(root)
		if root == "" {
			continue
		}
		if filepath.IsAbs(root) {
			rel, err := filepath.Rel(repoRoot, root)
			if err != nil {
				continue
			}
			root = rel
		}
		root = normalizePath(root)
		if root == "" {
			root = "."
		}
		normalized = append(normalized, root)
	}
	return normalized
}

func isWithinRoots(relPath string, roots []string) bool {
	for _, root := range roots {
		if root == "." {
			return true
		}
		if relPath == root || strings.HasPrefix(relPath, root+"/") {
			return true
		}
	}
	return false
}

func splitRoots(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	parts := strings.Split(value, ",")
	roots := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			roots = append(roots, part)
		}
	}
	if len(roots) == 0 {
		return nil
	}
	return roots
}

func reportViolations(pass *analysis.Pass, file *token.File, violations []Error) {
	if file == nil {
		return
	}
	for _, violation := range violations {
		if violation.Line <= 0 || violation.Line > file.LineCount() {
			continue
		}
		message := violation.Message
		if violation.Code != "" {
			message = message + " (code: " + violation.Code + ")"
		}
		pos := file.LineStart(violation.Line)
		pass.Report(analysis.Diagnostic{
			Pos:     pos,
			End:     lineEnd(file, violation.Line, pos),
			Message: message,
		})
	}
}

func lineEnd(file *token.File, line int, pos token.Pos) token.Pos {
	if line < file.LineCount() {
		nextLineStart := file.LineStart(line + 1)
		if nextLineStart > pos {
			return nextLineStart - 1
		}
	}

	if size := file.Size(); size > 0 {
		end := file.Pos(size)
		if end > pos {
			return end
		}
	}

	return pos
}
