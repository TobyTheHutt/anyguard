// Package validation validates and reports disallowed uses of the Go `any` type.
package validation

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const anyguardLinter = "anyguard"

const (
	rootWildcardSuffix = "/..."
	rootAllPattern     = "..."
	anyTokenMarker     = "<<ANY>>"
)

var nolintDirectiveRE = regexp.MustCompile(`(?i)\bnolint(?::([a-z0-9_,-]+))?`)

// Error represents a single disallowed `any` usage.
type Error struct {
	File    string
	Line    int
	Message string
	Code    string
}

// AnyAllowlist captures approved any-usage locations for enforcement.
type AnyAllowlist struct {
	Version      int                 `yaml:"version"`
	ExcludeGlobs []string            `yaml:"exclude_globs"`
	Entries      []AnyAllowlistEntry `yaml:"entries"`
}

// AnyAllowlistEntry describes a scoped any-usage exception.
type AnyAllowlistEntry struct {
	Path        string   `yaml:"path"`
	Symbols     []string `yaml:"symbols,omitempty"`
	Description string   `yaml:"description"`
	Refs        []string `yaml:"refs,omitempty"`
}

// LoadAnyAllowlist reads and validates a YAML any-usage allowlist file.
func LoadAnyAllowlist(listPath string) (AnyAllowlist, error) {
	// #nosec G304 -- repository tooling controls allowlist path.
	data, err := os.ReadFile(listPath)
	if err != nil {
		return AnyAllowlist{}, fmt.Errorf("read any allowlist: %w", err)
	}

	var allowlist AnyAllowlist
	unmarshalErr := yaml.Unmarshal(data, &allowlist)
	if unmarshalErr != nil {
		return AnyAllowlist{}, fmt.Errorf("parse any allowlist: %w", unmarshalErr)
	}
	validateErr := validateAllowlist(&allowlist)
	if validateErr != nil {
		return AnyAllowlist{}, validateErr
	}
	return allowlist, nil
}

// ValidateAnyUsageFromFile loads an allowlist and validates any-usage across roots.
func ValidateAnyUsageFromFile(listPath, baseDir string, roots []string) ([]Error, error) {
	allowlist, err := LoadAnyAllowlist(listPath)
	if err != nil {
		return nil, err
	}
	return ValidateAnyUsage(allowlist, baseDir, roots)
}

// ValidateAnyUsage reports any-type usages not covered by the provided allowlist.
func ValidateAnyUsage(allowlist AnyAllowlist, baseDir string, roots []string) ([]Error, error) {
	if len(roots) == 0 {
		return nil, errors.New(errNoRootsProvided)
	}
	if err := validateAllowlist(&allowlist); err != nil {
		return nil, err
	}

	baseAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, fmt.Errorf("resolve base dir: %w", err)
	}

	index := buildAllowlistIndex(allowlist)
	violations := make([]Error, 0)
	for _, root := range roots {
		rootPath, skipRoot, rootErr := resolveRootPath(baseAbs, root)
		if rootErr != nil {
			return nil, rootErr
		}
		if skipRoot {
			continue
		}

		rootViolations, validateErr := validateRoot(rootPath, baseAbs, allowlist.ExcludeGlobs, index)
		if validateErr != nil {
			return nil, validateErr
		}
		violations = append(violations, rootViolations...)
	}

	return violations, nil
}

func validateRoot(rootPath, baseAbs string, globs []string, index anyAllowlistIndex) ([]Error, error) {
	violations := make([]Error, 0)
	walkErr := filepath.WalkDir(rootPath, func(path string, entry fs.DirEntry, walkErr error) error {
		fileViolations, processErr := processRootFile(path, entry, walkErr, baseAbs, globs, index)
		if processErr != nil {
			return processErr
		}
		violations = append(violations, fileViolations...)
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return violations, nil
}

func processRootFile(path string, entry fs.DirEntry, walkErr error, baseAbs string, globs []string, index anyAllowlistIndex) ([]Error, error) {
	if walkErr != nil {
		return nil, walkErr
	}
	if entry.IsDir() || !strings.HasSuffix(path, ".go") {
		return nil, nil
	}

	relPath, relErr := filepath.Rel(baseAbs, path)
	if relErr != nil {
		return nil, relErr
	}
	relPath = normalizePath(relPath)
	if shouldExclude(relPath, globs) || index.allowAll[relPath] {
		return nil, nil
	}
	return validateAnyFile(path, relPath, index)
}

func resolveRootPath(baseAbs, root string) (string, bool, error) {
	root = normalizeRootValue(root)
	if root == "" {
		return "", true, nil
	}

	rootPath := root
	if !filepath.IsAbs(rootPath) {
		rootPath = filepath.Join(baseAbs, rootPath)
	}

	info, err := os.Stat(rootPath)
	if err != nil {
		return "", false, fmt.Errorf("stat root %s: %w", root, err)
	}
	if !info.IsDir() {
		return "", false, fmt.Errorf("root %s is not a directory", root)
	}
	return rootPath, false, nil
}

func normalizeRootValue(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}

	root = filepath.ToSlash(root)
	switch root {
	case rootAllPattern, DefaultRoots:
		return "."
	}

	if strings.HasSuffix(root, rootWildcardSuffix) {
		root = strings.TrimSuffix(root, rootWildcardSuffix)
		if root == "" {
			return "."
		}
	}
	return root
}

func validateAllowlist(allowlist *AnyAllowlist) error {
	if allowlist.Version <= 0 {
		return errors.New("any allowlist version must be >= 1")
	}

	for i, entry := range allowlist.Entries {
		entry.Path = normalizePath(entry.Path)
		if entry.Path == "" {
			return fmt.Errorf("any allowlist entry %d missing path", i)
		}

		entry.Description = strings.TrimSpace(entry.Description)
		if entry.Description == "" {
			return fmt.Errorf("any allowlist entry %d missing description", i)
		}

		entry.Symbols = normalizeSymbols(entry.Symbols)
		allowlist.Entries[i] = entry
	}

	for i, glob := range allowlist.ExcludeGlobs {
		allowlist.ExcludeGlobs[i] = strings.TrimSpace(glob)
	}
	return nil
}

func normalizePath(path string) string {
	cleaned := filepath.Clean(strings.TrimSpace(path))
	cleaned = filepath.ToSlash(cleaned)
	return strings.TrimPrefix(cleaned, "./")
}

func normalizeSymbols(symbols []string) []string {
	if len(symbols) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(symbols))
	for _, symbol := range symbols {
		symbol = strings.TrimSpace(symbol)
		if symbol != "" {
			normalized = append(normalized, symbol)
		}
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

type anyAllowlistIndex struct {
	allowAll map[string]bool
	symbols  map[string]map[string]struct{}
}

func buildAllowlistIndex(allowlist AnyAllowlist) anyAllowlistIndex {
	index := anyAllowlistIndex{
		allowAll: make(map[string]bool),
		symbols:  make(map[string]map[string]struct{}),
	}

	for _, entry := range allowlist.Entries {
		if len(entry.Symbols) == 0 {
			index.allowAll[entry.Path] = true
			continue
		}

		if _, ok := index.symbols[entry.Path]; !ok {
			index.symbols[entry.Path] = make(map[string]struct{})
		}
		for _, symbol := range entry.Symbols {
			index.symbols[entry.Path][symbol] = struct{}{}
		}
	}

	return index
}

func (index anyAllowlistIndex) isAllowed(relPath, symbol string) bool {
	if index.allowAll[relPath] {
		return true
	}
	if symbol == "" {
		return false
	}
	allowedSymbols, ok := index.symbols[relPath]
	if !ok {
		return false
	}
	_, ok = allowedSymbols[symbol]
	return ok
}

func validateAnyFile(path, relPath string, index anyAllowlistIndex) ([]Error, error) {
	// #nosec G304 -- path is discovered from validated roots.
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, content, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	typeParamRanges := collectTypeParamRanges(file)
	symbolRanges := buildSymbolRanges(file)
	nolintLines := collectNolintLines(file, fset)
	uses := collectAnyUsages(file, typeParamRanges)
	if len(uses) == 0 {
		return nil, nil
	}

	lines := strings.Split(string(content), "\n")
	violations := make([]Error, 0, len(uses))
	for _, usage := range uses {
		pos := fset.Position(usage.pos)
		if isSuppressedByNolint(pos.Line, nolintLines) {
			continue
		}

		symbol := symbolForPos(symbolRanges, usage.pos)
		if index.isAllowed(relPath, symbol) {
			continue
		}

		violations = append(violations, newViolation(relPath, pos.Line, lines))
	}

	return violations, nil
}

func newViolation(relPath string, line int, lines []string) Error {
	code := ""
	if line > 0 && line <= len(lines) {
		code = strings.TrimSpace(lines[line-1])
	}
	return Error{
		File:    relPath,
		Line:    line,
		Message: "disallowed any usage; add allowlist entry, use //nolint:anyguard, or replace with a concrete type",
		Code:    code,
	}
}

func collectNolintLines(file *ast.File, fset *token.FileSet) map[int]struct{} {
	lines := make(map[int]struct{})
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if !matchesAnyguardNolint(comment.Text) {
				continue
			}
			start := fset.Position(comment.Pos()).Line
			end := fset.Position(comment.End()).Line
			for line := start; line <= end; line++ {
				lines[line] = struct{}{}
			}
		}
	}
	return lines
}

func matchesAnyguardNolint(text string) bool {
	matches := nolintDirectiveRE.FindAllStringSubmatch(strings.ToLower(text), -1)
	for _, match := range matches {
		if len(match) < 2 || strings.TrimSpace(match[1]) == "" {
			return true
		}
		for _, token := range strings.Split(match[1], ",") {
			token = strings.TrimSpace(token)
			if token == "all" || token == anyguardLinter {
				return true
			}
		}
	}
	return false
}

func isSuppressedByNolint(line int, lines map[int]struct{}) bool {
	if line <= 0 {
		return false
	}
	if _, ok := lines[line]; ok {
		return true
	}
	_, ok := lines[line-1]
	return ok
}

type typeParamRange struct {
	start token.Pos
	end   token.Pos
}

func collectTypeParamRanges(file *ast.File) []typeParamRange {
	ranges := make([]typeParamRange, 0)
	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.FuncType:
			ranges = append(ranges, typeParamRanges(n.TypeParams)...)
		case *ast.TypeSpec:
			ranges = append(ranges, typeParamRanges(n.TypeParams)...)
		}
		return true
	})
	return ranges
}

func typeParamRanges(fields *ast.FieldList) []typeParamRange {
	if fields == nil {
		return nil
	}

	ranges := make([]typeParamRange, 0, len(fields.List))
	for _, field := range fields.List {
		if field == nil || field.Type == nil {
			continue
		}
		ranges = append(ranges, typeParamRange{
			start: field.Type.Pos(),
			end:   field.Type.End(),
		})
	}
	return ranges
}

type anyUsage struct {
	pos token.Pos
}

func collectAnyUsages(file *ast.File, constraints []typeParamRange) []anyUsage {
	uses := make([]anyUsage, 0)
	stack := make([]ast.Node, 0)
	ast.Inspect(file, func(node ast.Node) bool {
		if node == nil {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			return true
		}

		stack = append(stack, node)
		ident, ok := node.(*ast.Ident)
		if ok && ident.Name == "any" && isTypeIdent(stack) && !isInTypeParamRange(ident.Pos(), constraints) {
			uses = append(uses, anyUsage{pos: ident.Pos()})
		}
		return true
	})
	return uses
}

func isInTypeParamRange(pos token.Pos, ranges []typeParamRange) bool {
	for _, scope := range ranges {
		if pos >= scope.start && pos <= scope.end {
			return true
		}
	}
	return false
}

func isTypeIdent(stack []ast.Node) bool {
	if len(stack) < 2 {
		return false
	}

	parent := stack[len(stack)-2]
	child := stack[len(stack)-1]
	if isCompositeTypeContext(parent, child) {
		return true
	}
	if isDeclarationTypeContext(parent, child) {
		return true
	}
	return isIndexTypeContext(parent, child)
}

func isCompositeTypeContext(parent, child ast.Node) bool {
	switch n := parent.(type) {
	case *ast.ArrayType:
		return n.Elt == child
	case *ast.MapType:
		return n.Key == child || n.Value == child
	case *ast.ChanType:
		return n.Value == child
	case *ast.StarExpr:
		return n.X == child
	case *ast.Ellipsis:
		return n.Elt == child
	default:
		return false
	}
}

func isDeclarationTypeContext(parent, child ast.Node) bool {
	switch n := parent.(type) {
	case *ast.Field:
		return n.Type == child
	case *ast.ValueSpec:
		return n.Type == child
	case *ast.TypeSpec:
		return n.Type == child
	case *ast.TypeAssertExpr:
		return n.Type == child
	case *ast.CallExpr:
		return n.Fun == child
	default:
		return false
	}
}

func isIndexTypeContext(parent, child ast.Node) bool {
	switch n := parent.(type) {
	case *ast.IndexExpr:
		return n.Index == child
	case *ast.IndexListExpr:
		for _, index := range n.Indices {
			if index == child {
				return true
			}
		}
	}
	return false
}

type symbolRange struct {
	name  string
	start token.Pos
	end   token.Pos
}

func buildSymbolRanges(file *ast.File) []symbolRange {
	ranges := make([]symbolRange, 0)
	for _, decl := range file.Decls {
		switch node := decl.(type) {
		case *ast.GenDecl:
			ranges = append(ranges, symbolRangesForSpec(node.Specs)...)
		case *ast.FuncDecl:
			name := node.Name.Name
			if node.Recv != nil && len(node.Recv.List) > 0 {
				if recv := receiverTypeName(node.Recv.List[0].Type); recv != "" {
					name = recv
				}
			}
			ranges = append(ranges, symbolRange{name: name, start: node.Pos(), end: node.End()})
		}
	}
	return ranges
}

func symbolRangesForSpec(specs []ast.Spec) []symbolRange {
	ranges := make([]symbolRange, 0)
	for _, spec := range specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			ranges = append(ranges, symbolRange{name: s.Name.Name, start: s.Pos(), end: s.End()})
		case *ast.ValueSpec:
			for _, name := range s.Names {
				ranges = append(ranges, symbolRange{name: name.Name, start: s.Pos(), end: s.End()})
			}
		}
	}
	return ranges
}

func receiverTypeName(expr ast.Expr) string {
	switch node := expr.(type) {
	case *ast.Ident:
		return node.Name
	case *ast.StarExpr:
		return receiverTypeName(node.X)
	case *ast.IndexExpr:
		return receiverTypeName(node.X)
	case *ast.IndexListExpr:
		return receiverTypeName(node.X)
	default:
		return ""
	}
}

func symbolForPos(ranges []symbolRange, pos token.Pos) string {
	for _, scope := range ranges {
		if pos >= scope.start && pos <= scope.end {
			return scope.name
		}
	}
	return ""
}

func shouldExclude(relPath string, globs []string) bool {
	for _, glob := range globs {
		if glob == "" {
			continue
		}
		matched, err := matchGlob(glob, relPath)
		if err == nil && matched {
			return true
		}
	}
	return false
}

func matchGlob(pattern, value string) (bool, error) {
	pattern = normalizePath(pattern)
	value = normalizePath(value)

	escaped := regexp.QuoteMeta(pattern)
	escaped = strings.ReplaceAll(escaped, `\*\*`, anyTokenMarker)
	escaped = strings.ReplaceAll(escaped, `\*`, `[^/]*`)
	escaped = strings.ReplaceAll(escaped, `\?`, `[^/]`)
	escaped = strings.ReplaceAll(escaped, anyTokenMarker, ".*")

	expr := "^" + escaped + "$"
	re, err := regexp.Compile(expr)
	if err != nil {
		return false, err
	}
	return re.MatchString(value), nil
}
