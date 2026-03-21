// Package validation validates and reports disallowed uses of the Go `any` type.
package validation

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const anyguardLinter = "anyguard"

const (
	anyAllowlistVersion = 2
	rootWildcardSuffix  = "/..."
	rootAllPattern      = "..."
	anyTokenMarker      = "<<ANY>>"
	anyName             = "any"
	sourceImporterMode  = "source"
)

var nolintDirectiveRE = regexp.MustCompile(`(?i)\bnolint(?::([a-z0-9_,-]+))?`)

var universeAnyAlias = types.Universe.Lookup(anyName)

func resolvesToPredeclaredAny(ident *ast.Ident, info *types.Info) bool {
	if ident == nil || info == nil {
		return false
	}
	return info.ObjectOf(ident) == universeAnyAlias
}

// Error represents a single disallowed `any` usage.
type Error struct {
	File     string // File mirrors Identity.File for existing callers.
	Line     int
	Column   int
	Message  string
	Code     string
	Identity FindingIdentity
}

// FindingIdentity is the canonical identity for a collected any-usage finding.
type FindingIdentity struct {
	File     string
	Owner    string
	Category string
}

// AnyAllowlist captures approved any-usage locations for enforcement.
type AnyAllowlist struct {
	Version      int                 `yaml:"version"`
	ExcludeGlobs []string            `yaml:"exclude_globs"`
	Entries      []AnyAllowlistEntry `yaml:"entries"`
}

// AnyAllowlistSelector describes the canonical finding identity a strict allowlist
// entry must resolve to.
type AnyAllowlistSelector struct {
	Path     string `yaml:"path"`
	Owner    string `yaml:"owner"`
	Category string `yaml:"category"`
}

// AnyAllowlistEntry describes a scoped any-usage exception.
type AnyAllowlistEntry struct {
	Selector    *AnyAllowlistSelector `yaml:"selector"`
	Description string                `yaml:"description"`
	Refs        []string              `yaml:"refs,omitempty"`
}

// LoadAnyAllowlist reads and validates a YAML any-usage allowlist file.
func LoadAnyAllowlist(listPath string) (AnyAllowlist, error) {
	// #nosec G304 -- repository tooling controls allowlist path.
	data, err := os.ReadFile(listPath)
	if err != nil {
		return AnyAllowlist{}, fmt.Errorf("read any allowlist: %w", err)
	}

	var allowlist AnyAllowlist
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	unmarshalErr := decoder.Decode(&allowlist)
	if unmarshalErr != nil {
		return AnyAllowlist{}, wrapAllowlistParseError(unmarshalErr)
	}
	validateErr := validateAllowlist(&allowlist)
	if validateErr != nil {
		return AnyAllowlist{}, validateErr
	}
	return allowlist, nil
}

func wrapAllowlistParseError(err error) error {
	message := err.Error()
	if strings.Contains(message, "field path not found") || strings.Contains(message, "field symbols not found") {
		return fmt.Errorf("parse any allowlist: legacy allowlist entry shape is unsupported: %w", err)
	}
	return fmt.Errorf("parse any allowlist: %w", err)
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

	findings, err := collectFindings(baseAbs, roots, allowlist.ExcludeGlobs)
	if err != nil {
		return nil, err
	}

	index, err := resolveAllowlistIndex(allowlist, findings)
	if err != nil {
		return nil, err
	}

	return violationsFromFindings(findings, index), nil
}

func collectFindings(baseAbs string, roots, globs []string) ([]collectedFinding, error) {
	findings := make([]collectedFinding, 0)
	for _, root := range roots {
		rootPath, skipRoot, rootErr := resolveRootPath(baseAbs, root)
		if rootErr != nil {
			return nil, rootErr
		}
		if skipRoot {
			continue
		}

		rootFindings, err := collectRootFindings(rootPath, baseAbs, globs)
		if err != nil {
			return nil, err
		}
		findings = append(findings, rootFindings...)
	}
	sortCollectedFindings(findings)
	return findings, nil
}

func collectRootFindings(rootPath, baseAbs string, globs []string) ([]collectedFinding, error) {
	parsed, err := collectParsedPackages(rootPath, baseAbs, globs)
	if err != nil {
		return nil, err
	}

	findings := make([]collectedFinding, 0)
	sourceImporter := importer.ForCompiler(parsed.fset, sourceImporterMode, nil)
	for _, parsedPackage := range parsed.packages {
		info := typeCheckParsedPackage(parsed.fset, sourceImporter, parsedPackage)
		for _, file := range parsedPackage.files {
			findings = append(findings, collectParsedFileFindings(parsed.fset, info, file)...)
		}
	}
	return findings, nil
}

type parsedGoFile struct {
	relPath string
	content []byte
	syntax  *ast.File
}

type parsedPackageCollection struct {
	fset     *token.FileSet
	packages []parsedPackage
}

type parsedPackageKey struct {
	dir  string
	name string
}

type parsedPackage struct {
	dir   string
	name  string
	files []parsedGoFile
}

func collectParsedPackages(rootPath, baseAbs string, globs []string) (parsedPackageCollection, error) {
	fset := token.NewFileSet()
	grouped := make(map[parsedPackageKey]*parsedPackage)

	err := walkParsedFiles(rootPath, baseAbs, globs, fset, func(file parsedGoFile) error {
		appendParsedPackageFile(grouped, file)
		return nil
	})
	if err != nil {
		return parsedPackageCollection{}, err
	}

	return finalizeParsedPackages(fset, grouped), nil
}

func walkParsedFiles(
	rootPath, baseAbs string,
	globs []string,
	fset *token.FileSet,
	visit func(parsedGoFile) error,
) error {
	return filepath.WalkDir(rootPath, func(path string, entry fs.DirEntry, walkErr error) error {
		parsedFile, keep, err := parseRootFile(fset, path, entry, walkErr, baseAbs, globs)
		if err != nil {
			return err
		}
		if !keep {
			return nil
		}
		return visit(parsedFile)
	})
}

func appendParsedPackageFile(grouped map[parsedPackageKey]*parsedPackage, file parsedGoFile) {
	key := newParsedPackageKey(file)
	group, ok := grouped[key]
	if !ok {
		group = &parsedPackage{
			dir:   key.dir,
			name:  key.name,
			files: make([]parsedGoFile, 0),
		}
		grouped[key] = group
	}
	group.files = append(group.files, file)
}

func finalizeParsedPackages(
	fset *token.FileSet,
	grouped map[parsedPackageKey]*parsedPackage,
) parsedPackageCollection {
	packages := make([]parsedPackage, 0, len(grouped))
	for _, group := range grouped {
		sort.Slice(group.files, func(i, j int) bool {
			return group.files[i].relPath < group.files[j].relPath
		})
		packages = append(packages, *group)
	}
	sort.Slice(packages, func(i, j int) bool {
		left := packages[i]
		right := packages[j]
		switch {
		case left.dir != right.dir:
			return left.dir < right.dir
		default:
			return left.name < right.name
		}
	})
	return parsedPackageCollection{
		fset:     fset,
		packages: packages,
	}
}

func parseRootFile(
	fset *token.FileSet,
	path string,
	entry fs.DirEntry,
	walkErr error,
	baseAbs string,
	globs []string,
) (parsedGoFile, bool, error) {
	if walkErr != nil {
		return parsedGoFile{}, false, walkErr
	}
	if entry.IsDir() || !strings.HasSuffix(path, ".go") {
		return parsedGoFile{}, false, nil
	}

	relPath, relErr := filepath.Rel(baseAbs, path)
	if relErr != nil {
		return parsedGoFile{}, false, relErr
	}
	relPath = normalizePath(relPath)
	if shouldExclude(relPath, globs) {
		return parsedGoFile{}, false, nil
	}

	// #nosec G304 -- path is discovered from validated roots.
	content, err := os.ReadFile(path)
	if err != nil {
		return parsedGoFile{}, false, err
	}

	syntax, err := parser.ParseFile(fset, path, content, parser.ParseComments)
	if err != nil {
		return parsedGoFile{}, false, err
	}

	return parsedGoFile{
		relPath: relPath,
		content: content,
		syntax:  syntax,
	}, true, nil
}

func newParsedPackageKey(file parsedGoFile) parsedPackageKey {
	return parsedPackageKey{
		dir:  filepath.Dir(file.relPath),
		name: file.syntax.Name.Name,
	}
}

func typeCheckParsedPackage(fset *token.FileSet, sourceImporter types.Importer, pkg parsedPackage) *types.Info {
	files := make([]*ast.File, 0, len(pkg.files))
	for _, file := range pkg.files {
		files = append(files, file.syntax)
	}

	info := &types.Info{
		Uses: make(map[*ast.Ident]types.Object),
	}

	packagePath := pkg.dir
	if packagePath == "" || packagePath == "." {
		packagePath = pkg.name
	}

	config := types.Config{
		DisableUnusedImportCheck: true,
		Error:                    func(error) {},
		Importer:                 sourceImporter,
	}
	// Keep partial type info on invalid packages so shadowed or unresolved names stay silent.
	if _, err := config.Check(packagePath, fset, files, info); err != nil {
		slog.Debug("anyguard partial type-check failed", "package_path", packagePath, "error", err)
		return info
	}
	return info
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
	if allowlist.Version != anyAllowlistVersion {
		return fmt.Errorf("unsupported any allowlist version %d: expected %d", allowlist.Version, anyAllowlistVersion)
	}

	seenSelectors := make(map[FindingIdentity]int, len(allowlist.Entries))
	for i, entry := range allowlist.Entries {
		normalizedEntry, err := validateAllowlistEntry(entry, i, seenSelectors)
		if err != nil {
			return err
		}
		allowlist.Entries[i] = normalizedEntry
	}

	for i, glob := range allowlist.ExcludeGlobs {
		allowlist.ExcludeGlobs[i] = strings.TrimSpace(glob)
	}
	return nil
}

func validateAllowlistEntry(entry AnyAllowlistEntry, index int, seenSelectors map[FindingIdentity]int) (AnyAllowlistEntry, error) {
	selector, err := normalizeValidatedSelector(entry.Selector, index)
	if err != nil {
		return AnyAllowlistEntry{}, err
	}

	entry.Description = strings.TrimSpace(entry.Description)
	if entry.Description == "" {
		return AnyAllowlistEntry{}, fmt.Errorf("any allowlist entry %d missing description", index)
	}

	identity := selector.identity()
	if prev, exists := seenSelectors[identity]; exists {
		return AnyAllowlistEntry{}, fmt.Errorf(
			"any allowlist entries %d and %d resolve to the same selector %s",
			prev,
			index,
			formatFindingIdentity(identity),
		)
	}

	seenSelectors[identity] = index
	entry.Selector = &selector
	return entry, nil
}

func normalizeValidatedSelector(selector *AnyAllowlistSelector, index int) (AnyAllowlistSelector, error) {
	if selector == nil {
		return AnyAllowlistSelector{}, fmt.Errorf("any allowlist entry %d missing selector", index)
	}

	normalized := normalizeAllowlistSelector(*selector)
	if normalized.Path == "" {
		return AnyAllowlistSelector{}, fmt.Errorf("any allowlist entry %d selector missing path", index)
	}
	if normalized.Owner == "" {
		return AnyAllowlistSelector{}, fmt.Errorf("any allowlist entry %d selector missing owner", index)
	}
	if normalized.Category == "" {
		return AnyAllowlistSelector{}, fmt.Errorf("any allowlist entry %d selector missing category", index)
	}
	if !isSupportedAnyUsageCategory(normalized.Category) {
		return AnyAllowlistSelector{}, fmt.Errorf(
			"any allowlist entry %d selector has unknown category %q",
			index,
			normalized.Category,
		)
	}
	return normalized, nil
}

func normalizePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	cleaned := filepath.Clean(path)
	cleaned = filepath.ToSlash(cleaned)
	return strings.TrimPrefix(cleaned, "./")
}

func normalizeAllowlistSelector(selector AnyAllowlistSelector) AnyAllowlistSelector {
	selector.Path = normalizePath(selector.Path)
	selector.Owner = strings.TrimSpace(selector.Owner)
	selector.Category = strings.TrimSpace(selector.Category)
	return selector
}

type anyAllowlistIndex struct {
	allowed map[FindingIdentity]struct{}
}

func buildAllowlistIndex(allowlist AnyAllowlist) anyAllowlistIndex {
	index := anyAllowlistIndex{
		allowed: make(map[FindingIdentity]struct{}, len(allowlist.Entries)),
	}

	for _, entry := range allowlist.Entries {
		index.allowed[entry.Selector.identity()] = struct{}{}
	}

	return index
}

func (index anyAllowlistIndex) isAllowed(identity FindingIdentity) bool {
	_, ok := index.allowed[identity]
	return ok
}

type collectedFinding struct {
	identity           FindingIdentity
	line               int
	column             int
	code               string
	suppressedByNolint bool
}

func collectParsedFileFindings(fset *token.FileSet, info *types.Info, file parsedGoFile) []collectedFinding {
	nolintLines := collectNolintLines(file.syntax, fset)
	uses := collectAnyUsages(file.relPath, file.syntax, info)
	if len(uses) == 0 {
		return nil
	}

	lines := strings.Split(string(file.content), "\n")
	findings := make([]collectedFinding, 0, len(uses))
	for _, usage := range uses {
		pos := fset.Position(usage.pos)
		findings = append(findings, collectedFinding{
			identity:           usage.identity,
			line:               pos.Line,
			column:             pos.Column,
			code:               lineCode(pos.Line, lines),
			suppressedByNolint: isSuppressedByNolint(pos.Line, nolintLines),
		})
	}

	return findings
}

func lineCode(line int, lines []string) string {
	index := line - 1
	if index < 0 || index >= len(lines) {
		return ""
	}
	return strings.TrimSpace(lines[index])
}

func newViolation(finding collectedFinding) Error {
	return Error{
		File:     finding.identity.File,
		Line:     finding.line,
		Column:   finding.column,
		Message:  "disallowed any usage. Add an allowlist entry, use //nolint:anyguard, or replace it with a concrete type",
		Code:     finding.code,
		Identity: finding.identity,
	}
}

func violationsFromFindings(findings []collectedFinding, index anyAllowlistIndex) []Error {
	violations := make([]Error, 0, len(findings))
	for _, finding := range findings {
		if finding.suppressedByNolint || index.isAllowed(finding.identity) {
			continue
		}
		violations = append(violations, newViolation(finding))
	}
	sortViolations(violations)
	return violations
}

func sortCollectedFindings(findings []collectedFinding) {
	sort.Slice(findings, func(i, j int) bool {
		left := findings[i]
		right := findings[j]

		switch {
		case left.identity.File != right.identity.File:
			return left.identity.File < right.identity.File
		case left.line != right.line:
			return left.line < right.line
		case left.column != right.column:
			return left.column < right.column
		case left.identity.Category != right.identity.Category:
			return left.identity.Category < right.identity.Category
		case left.identity.Owner != right.identity.Owner:
			return left.identity.Owner < right.identity.Owner
		case left.code != right.code:
			return left.code < right.code
		default:
			return !left.suppressedByNolint && right.suppressedByNolint
		}
	})
}

func sortViolations(violations []Error) {
	sort.Slice(violations, func(i, j int) bool {
		left := violations[i]
		right := violations[j]

		switch {
		case left.File != right.File:
			return left.File < right.File
		case left.Line != right.Line:
			return left.Line < right.Line
		case left.Column != right.Column:
			return left.Column < right.Column
		case left.Identity.Category != right.Identity.Category:
			return left.Identity.Category < right.Identity.Category
		default:
			return left.Identity.Owner < right.Identity.Owner
		}
	})
}

func resolveAllowlistIndex(allowlist AnyAllowlist, findings []collectedFinding) (anyAllowlistIndex, error) {
	available := make(map[FindingIdentity]struct{}, len(findings))
	for _, finding := range findings {
		available[finding.identity] = struct{}{}
	}

	for i, entry := range allowlist.Entries {
		identity := entry.Selector.identity()
		if _, ok := available[identity]; ok {
			continue
		}
		return anyAllowlistIndex{}, fmt.Errorf(
			"any allowlist entry %d selector %s does not match any finding",
			i,
			formatFindingIdentity(identity),
		)
	}

	return buildAllowlistIndex(allowlist), nil
}

func (selector AnyAllowlistSelector) identity() FindingIdentity {
	return FindingIdentity{
		File:     selector.Path,
		Owner:    selector.Owner,
		Category: selector.Category,
	}
}

func formatFindingIdentity(identity FindingIdentity) string {
	return fmt.Sprintf("{path=%q owner=%q category=%q}", identity.File, identity.Owner, identity.Category)
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

type anyUsageCategory string

const (
	anyCategoryFieldType      anyUsageCategory = "*ast.Field.Type"
	anyCategoryValueSpecType  anyUsageCategory = "*ast.ValueSpec.Type"
	anyCategoryTypeSpecType   anyUsageCategory = "*ast.TypeSpec.Type"
	anyCategoryTypeAssertType anyUsageCategory = "*ast.TypeAssertExpr.Type"
	anyCategoryArrayTypeElt   anyUsageCategory = "*ast.ArrayType.Elt"
	anyCategoryMapTypeKey     anyUsageCategory = "*ast.MapType.Key"
	anyCategoryMapTypeValue   anyUsageCategory = "*ast.MapType.Value"
	anyCategoryChanTypeValue  anyUsageCategory = "*ast.ChanType.Value"
	anyCategoryStarExprX      anyUsageCategory = "*ast.StarExpr.X"
	anyCategoryEllipsisElt    anyUsageCategory = "*ast.Ellipsis.Elt"
	anyCategoryCallExprFun    anyUsageCategory = "*ast.CallExpr.Fun"
	anyCategoryIndexExprIndex anyUsageCategory = "*ast.IndexExpr.Index"
	anyCategoryIndexListIndex anyUsageCategory = "*ast.IndexListExpr.Indices"
)

func isSupportedAnyUsageCategory(category string) bool {
	switch anyUsageCategory(category) {
	case anyCategoryFieldType,
		anyCategoryValueSpecType,
		anyCategoryTypeSpecType,
		anyCategoryTypeAssertType,
		anyCategoryArrayTypeElt,
		anyCategoryMapTypeKey,
		anyCategoryMapTypeValue,
		anyCategoryChanTypeValue,
		anyCategoryStarExprX,
		anyCategoryEllipsisElt,
		anyCategoryCallExprFun,
		anyCategoryIndexExprIndex,
		anyCategoryIndexListIndex:
		return true
	default:
		return false
	}
}

type anyUsage struct {
	identity FindingIdentity
	pos      token.Pos
}

// anyUsageCollector records findings only from explicitly supported AST slots.
type anyUsageCollector struct {
	file   string
	info   *types.Info
	usages []anyUsage
}

func collectAnyUsages(relPath string, file *ast.File, info *types.Info) []anyUsage {
	collector := anyUsageCollector{
		file:   normalizePath(relPath),
		info:   info,
		usages: make([]anyUsage, 0),
	}
	collector.inspectFile(file)
	return collector.usages
}

func (collector *anyUsageCollector) inspectFile(file *ast.File) {
	if file == nil {
		return
	}
	for _, decl := range file.Decls {
		collector.inspectTopLevelDecl(decl)
	}
}

func (collector *anyUsageCollector) inspectTopLevelDecl(decl ast.Decl) {
	switch node := decl.(type) {
	case *ast.GenDecl:
		for _, spec := range node.Specs {
			collector.inspectTopLevelSpec(spec)
		}
	case *ast.FuncDecl:
		owner := funcDeclOwner(node)
		collector.inspectReceiverList(node.Recv, owner)
		collector.inspectFuncType(node.Type, owner)
		collector.inspectNode(node.Body, owner)
	}
}

func (collector *anyUsageCollector) inspectLocalDecl(decl ast.Decl, owner string) {
	genDecl, ok := decl.(*ast.GenDecl)
	if !ok {
		return
	}
	for _, spec := range genDecl.Specs {
		collector.inspectLocalSpec(spec, owner)
	}
}

func (collector *anyUsageCollector) inspectTopLevelSpec(spec ast.Spec) {
	switch node := spec.(type) {
	case *ast.TypeSpec:
		collector.visitSupportedSlot(anyCategoryTypeSpecType, node.Name.Name, node.Type)
	case *ast.ValueSpec:
		owner := valueSpecOwner(node)
		collector.visitSupportedSlot(anyCategoryValueSpecType, owner, node.Type)
		collector.inspectExprs(node.Values, owner)
	}
}

func (collector *anyUsageCollector) inspectLocalSpec(spec ast.Spec, owner string) {
	switch node := spec.(type) {
	case *ast.TypeSpec:
		collector.visitSupportedSlot(anyCategoryTypeSpecType, owner, node.Type)
	case *ast.ValueSpec:
		collector.visitSupportedSlot(anyCategoryValueSpecType, owner, node.Type)
		collector.inspectExprs(node.Values, owner)
	}
}

func (collector *anyUsageCollector) inspectFuncType(funcType *ast.FuncType, owner string) {
	if funcType == nil {
		return
	}
	collector.inspectFieldList(funcType.Params, owner)
	collector.inspectFieldList(funcType.Results, owner)
}

func (collector *anyUsageCollector) inspectReceiverList(receivers *ast.FieldList, owner string) {
	if receivers == nil {
		return
	}
	for _, field := range receivers.List {
		if field == nil {
			continue
		}
		collector.inspectNode(field.Type, owner)
	}
}

func (collector *anyUsageCollector) inspectFieldList(fields *ast.FieldList, owner string) {
	if fields == nil {
		return
	}
	for _, field := range fields.List {
		if field == nil {
			continue
		}
		collector.visitSupportedSlot(anyCategoryFieldType, owner, field.Type)
	}
}

func (collector *anyUsageCollector) inspectExprs(exprs []ast.Expr, owner string) {
	for _, expr := range exprs {
		collector.inspectNode(expr, owner)
	}
}

func (collector *anyUsageCollector) inspectStmts(stmts []ast.Stmt, owner string) {
	for _, stmt := range stmts {
		collector.inspectNode(stmt, owner)
	}
}

func (collector *anyUsageCollector) visitSupportedSlot(category anyUsageCategory, owner string, expr ast.Expr) {
	if expr == nil {
		return
	}
	ident, ok := expr.(*ast.Ident)
	if ok && resolvesToPredeclaredAny(ident, collector.info) {
		collector.usages = append(collector.usages, anyUsage{
			identity: newFindingIdentity(collector.file, owner, category),
			pos:      ident.Pos(),
		})
	}
	collector.inspectNode(expr, owner)
}

func newFindingIdentity(relPath, owner string, category anyUsageCategory) FindingIdentity {
	return FindingIdentity{
		File:     relPath,
		Owner:    owner,
		Category: string(category),
	}
}

func (collector *anyUsageCollector) inspectNode(node ast.Node, owner string) {
	if node == nil {
		return
	}
	ast.Inspect(node, func(current ast.Node) bool {
		return collector.inspectCurrentNode(current, owner)
	})
}

func (collector *anyUsageCollector) inspectCurrentNode(current ast.Node, owner string) bool {
	if current == nil {
		return false
	}
	if collector.inspectLeafNode(current) {
		return false
	}
	if collector.inspectDeclNode(current, owner) {
		return false
	}
	if collector.inspectTypeNode(current, owner) {
		return false
	}
	if collector.inspectExprNode(current, owner) {
		return false
	}
	if collector.inspectSimpleStmtNode(current, owner) {
		return false
	}
	if collector.inspectControlStmtNode(current, owner) {
		return false
	}
	return true
}

func (collector *anyUsageCollector) inspectLeafNode(node ast.Node) bool {
	switch node.(type) {
	case *ast.BadDecl, *ast.BadExpr, *ast.BadStmt, *ast.BasicLit, *ast.BranchStmt, *ast.EmptyStmt, *ast.Ident:
		return true
	default:
		return false
	}
}

func (collector *anyUsageCollector) inspectDeclNode(node ast.Node, owner string) bool {
	genDecl, ok := node.(*ast.GenDecl)
	if !ok {
		return false
	}
	collector.inspectLocalDecl(genDecl, owner)
	return true
}

func (collector *anyUsageCollector) inspectTypeNode(node ast.Node, owner string) bool {
	switch typed := node.(type) {
	case *ast.FuncType:
		collector.inspectFuncType(typed, owner)
	case *ast.FuncLit:
		collector.inspectFuncType(typed.Type, owner)
		collector.inspectNode(typed.Body, owner)
	case *ast.StructType:
		collector.inspectFieldList(typed.Fields, owner)
	case *ast.InterfaceType:
		collector.inspectFieldList(typed.Methods, owner)
	case *ast.ArrayType:
		collector.inspectNode(typed.Len, owner)
		collector.visitSupportedSlot(anyCategoryArrayTypeElt, owner, typed.Elt)
	case *ast.MapType:
		collector.visitSupportedSlot(anyCategoryMapTypeKey, owner, typed.Key)
		collector.visitSupportedSlot(anyCategoryMapTypeValue, owner, typed.Value)
	case *ast.ChanType:
		collector.visitSupportedSlot(anyCategoryChanTypeValue, owner, typed.Value)
	case *ast.StarExpr:
		collector.visitSupportedSlot(anyCategoryStarExprX, owner, typed.X)
	case *ast.Ellipsis:
		collector.visitSupportedSlot(anyCategoryEllipsisElt, owner, typed.Elt)
	default:
		return false
	}
	return true
}

func (collector *anyUsageCollector) inspectExprNode(node ast.Node, owner string) bool {
	switch typed := node.(type) {
	case *ast.CallExpr:
		collector.visitSupportedSlot(anyCategoryCallExprFun, owner, typed.Fun)
		collector.inspectExprs(typed.Args, owner)
	case *ast.TypeAssertExpr:
		collector.inspectNode(typed.X, owner)
		collector.visitSupportedSlot(anyCategoryTypeAssertType, owner, typed.Type)
	case *ast.IndexExpr:
		collector.inspectNode(typed.X, owner)
		collector.visitSupportedSlot(anyCategoryIndexExprIndex, owner, typed.Index)
	case *ast.IndexListExpr:
		collector.inspectNode(typed.X, owner)
		for _, index := range typed.Indices {
			collector.visitSupportedSlot(anyCategoryIndexListIndex, owner, index)
		}
	case *ast.CompositeLit:
		collector.inspectNode(typed.Type, owner)
		collector.inspectExprs(typed.Elts, owner)
	case *ast.KeyValueExpr:
		collector.inspectNode(typed.Key, owner)
		collector.inspectNode(typed.Value, owner)
	case *ast.ParenExpr:
		collector.inspectNode(typed.X, owner)
	case *ast.SelectorExpr:
		collector.inspectNode(typed.X, owner)
	case *ast.SliceExpr:
		collector.inspectNode(typed.X, owner)
		collector.inspectNode(typed.Low, owner)
		collector.inspectNode(typed.High, owner)
		collector.inspectNode(typed.Max, owner)
	case *ast.UnaryExpr:
		collector.inspectNode(typed.X, owner)
	case *ast.BinaryExpr:
		collector.inspectNode(typed.X, owner)
		collector.inspectNode(typed.Y, owner)
	default:
		return false
	}
	return true
}

func (collector *anyUsageCollector) inspectSimpleStmtNode(node ast.Node, owner string) bool {
	switch typed := node.(type) {
	case *ast.BlockStmt:
		collector.inspectStmts(typed.List, owner)
	case *ast.LabeledStmt:
		collector.inspectNode(typed.Stmt, owner)
	case *ast.DeclStmt:
		collector.inspectLocalDecl(typed.Decl, owner)
	case *ast.ExprStmt:
		collector.inspectNode(typed.X, owner)
	case *ast.SendStmt:
		collector.inspectNode(typed.Chan, owner)
		collector.inspectNode(typed.Value, owner)
	case *ast.IncDecStmt:
		collector.inspectNode(typed.X, owner)
	case *ast.AssignStmt:
		collector.inspectExprs(typed.Lhs, owner)
		collector.inspectExprs(typed.Rhs, owner)
	case *ast.GoStmt:
		collector.inspectNode(typed.Call, owner)
	case *ast.DeferStmt:
		collector.inspectNode(typed.Call, owner)
	case *ast.ReturnStmt:
		collector.inspectExprs(typed.Results, owner)
	default:
		return false
	}
	return true
}

func (collector *anyUsageCollector) inspectControlStmtNode(node ast.Node, owner string) bool {
	switch typed := node.(type) {
	case *ast.IfStmt:
		collector.inspectNode(typed.Init, owner)
		collector.inspectNode(typed.Cond, owner)
		collector.inspectNode(typed.Body, owner)
		collector.inspectNode(typed.Else, owner)
	case *ast.SwitchStmt:
		collector.inspectNode(typed.Init, owner)
		collector.inspectNode(typed.Tag, owner)
		collector.inspectNode(typed.Body, owner)
	case *ast.TypeSwitchStmt:
		collector.inspectNode(typed.Init, owner)
		collector.inspectNode(typed.Assign, owner)
		collector.inspectTypeSwitchBody(typed.Body, owner)
	case *ast.CaseClause:
		collector.inspectExprs(typed.List, owner)
		collector.inspectStmts(typed.Body, owner)
	case *ast.CommClause:
		collector.inspectNode(typed.Comm, owner)
		collector.inspectStmts(typed.Body, owner)
	case *ast.SelectStmt:
		collector.inspectNode(typed.Body, owner)
	case *ast.ForStmt:
		collector.inspectNode(typed.Init, owner)
		collector.inspectNode(typed.Cond, owner)
		collector.inspectNode(typed.Post, owner)
		collector.inspectNode(typed.Body, owner)
	case *ast.RangeStmt:
		collector.inspectNode(typed.Key, owner)
		collector.inspectNode(typed.Value, owner)
		collector.inspectNode(typed.X, owner)
		collector.inspectNode(typed.Body, owner)
	default:
		return false
	}
	return true
}

func (collector *anyUsageCollector) inspectTypeSwitchBody(body *ast.BlockStmt, owner string) {
	if body == nil {
		return
	}
	for _, stmt := range body.List {
		clause, ok := stmt.(*ast.CaseClause)
		if !ok {
			collector.inspectNode(stmt, owner)
			continue
		}
		collector.inspectStmts(clause.Body, owner)
	}
}

func valueSpecOwner(spec *ast.ValueSpec) string {
	if spec == nil {
		return ""
	}
	for _, name := range spec.Names {
		if name != nil {
			return name.Name
		}
	}
	return ""
}

func funcDeclOwner(decl *ast.FuncDecl) string {
	if decl == nil || decl.Name == nil {
		return ""
	}
	owner := decl.Name.Name
	if decl.Recv != nil && len(decl.Recv.List) > 0 {
		if recv := receiverTypeName(decl.Recv.List[0].Type); recv != "" {
			owner = recv
		}
	}
	return owner
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
