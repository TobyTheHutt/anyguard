// Package validation validates and reports disallowed uses of the Go `any` type.
package validation

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
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
	anyAllowlistVersion   = 2
	rootWildcardSuffix    = "/..."
	rootAllPattern        = "..."
	anyTokenMarker        = "<<ANY>>"
	anyName               = "any"
	goflagsEnvVar         = "GOFLAGS"
	goosEnvVar            = "GOOS"
	goarchEnvVar          = "GOARCH"
	cgoEnabledEnvVar      = "CGO_ENABLED"
	logAttrError          = "error"
	errDuplicateSelector  = "any allowlist entries %d and %d resolve to the same selector %s"
	errUnresolvedSelector = "any allowlist entry %d selector %s does not match any finding"
)

var nolintDirectiveRE = regexp.MustCompile(`(?i)\bnolint(?::([a-z0-9_,-]+))?`)

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
	Line     int
	Column   int
}

// AnyAllowlist captures approved any-usage locations for enforcement.
type AnyAllowlist struct {
	Version      int                 `yaml:"version"`
	ExcludeGlobs []string            `yaml:"exclude_globs"`
	Entries      []AnyAllowlistEntry `yaml:"entries"`
}

// AnyAllowlistSelector describes the canonical finding identity a strict allowlist
// entry must resolve to. Line and column may be omitted only for legacy
// selectors that still resolve uniquely.
type AnyAllowlistSelector struct {
	Path     string `yaml:"path"`
	Owner    string `yaml:"owner"`
	Category string `yaml:"category"`
	Line     int    `yaml:"line"`
	Column   int    `yaml:"column"`
}

// AnyAllowlistEntry describes a scoped any-usage exception.
type AnyAllowlistEntry struct {
	Selector    *AnyAllowlistSelector `yaml:"selector"`
	Description string                `yaml:"description"`
	Refs        []string              `yaml:"refs,omitempty"`
}

type loadedAnyAllowlist struct {
	allowlist    AnyAllowlist
	excludeGlobs compiledExcludeGlobs
	fingerprint  string
}

// LoadAnyAllowlist reads and validates a YAML any-usage allowlist file.
func LoadAnyAllowlist(listPath string) (AnyAllowlist, error) {
	loaded, err := loadAnyAllowlist(listPath)
	if err != nil {
		return AnyAllowlist{}, err
	}
	return loaded.allowlist, nil
}

func loadAnyAllowlist(listPath string) (loadedAnyAllowlist, error) {
	// #nosec G304 -- repository tooling controls allowlist path.
	data, err := os.ReadFile(listPath)
	if err != nil {
		return loadedAnyAllowlist{}, fmt.Errorf("read any allowlist: %w", err)
	}

	allowlist, err := decodeAnyAllowlist(data)
	if err != nil {
		return loadedAnyAllowlist{}, err
	}

	excludeGlobs, err := compileExcludeGlobs(allowlist.ExcludeGlobs)
	if err != nil {
		return loadedAnyAllowlist{}, err
	}

	return loadedAnyAllowlist{
		allowlist:    allowlist,
		excludeGlobs: excludeGlobs,
		fingerprint:  fingerprintAnyAllowlistData(data),
	}, nil
}

func decodeAnyAllowlist(data []byte) (AnyAllowlist, error) {
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

func fingerprintAnyAllowlistData(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:])
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
	loaded, err := loadAnyAllowlist(listPath)
	if err != nil {
		return nil, err
	}
	return validateAnyUsageWithValidatedAllowlist(loaded.allowlist, loaded.excludeGlobs, baseDir, roots)
}

// ValidateAnyUsage reports any-type usages not covered by the provided allowlist.
func ValidateAnyUsage(allowlist AnyAllowlist, baseDir string, roots []string) ([]Error, error) {
	if len(roots) == 0 {
		return nil, errors.New(errNoRootsProvided)
	}
	if err := validateAllowlist(&allowlist); err != nil {
		return nil, err
	}

	excludeGlobs, err := compileExcludeGlobs(allowlist.ExcludeGlobs)
	if err != nil {
		return nil, err
	}
	return validateAnyUsageWithValidatedAllowlist(allowlist, excludeGlobs, baseDir, roots)
}

func validateAnyUsageWithValidatedAllowlist(
	allowlist AnyAllowlist,
	excludeGlobs compiledExcludeGlobs,
	baseDir string,
	roots []string,
) ([]Error, error) {
	if len(roots) == 0 {
		return nil, errors.New(errNoRootsProvided)
	}

	baseAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, fmt.Errorf("resolve base dir: %w", err)
	}

	buildCtx := currentBuildContext()
	findings, err := collectFindingsWithCompiledExcludeGlobs(baseAbs, roots, excludeGlobs, buildCtx)
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
	return collectFindingsWithBuildContext(baseAbs, roots, globs, currentBuildContext())
}

func collectFindingsWithBuildContext(baseAbs string, roots, globs []string, buildCtx *build.Context) ([]collectedFinding, error) {
	excludeGlobs, err := compileExcludeGlobs(globs)
	if err != nil {
		return nil, err
	}
	return collectFindingsWithCompiledExcludeGlobs(baseAbs, roots, excludeGlobs, buildCtx)
}

func collectFindingsWithCompiledExcludeGlobs(
	baseAbs string,
	roots []string,
	excludeGlobs compiledExcludeGlobs,
	buildCtx *build.Context,
) ([]collectedFinding, error) {
	if buildCtx == nil {
		buildCtx = currentBuildContext()
	}

	findings := make([]collectedFinding, 0)
	for _, root := range roots {
		rootPath, skipRoot, rootErr := resolveRootPath(baseAbs, root)
		if rootErr != nil {
			return nil, rootErr
		}
		if skipRoot {
			continue
		}

		rootFindings, err := collectRootFindings(rootPath, baseAbs, excludeGlobs, buildCtx)
		if err != nil {
			return nil, err
		}
		findings = append(findings, rootFindings...)
	}
	sortCollectedFindings(findings)
	return findings, nil
}

func collectRootFindings(
	rootPath string,
	baseAbs string,
	excludeGlobs compiledExcludeGlobs,
	buildCtx *build.Context,
) ([]collectedFinding, error) {
	parsed, err := collectParsedPackages(rootPath, baseAbs, excludeGlobs, buildCtx)
	if err != nil {
		return nil, err
	}

	findings := make([]collectedFinding, 0)
	for _, parsedPackage := range parsed.packages {
		resolver := newParsedPackageLexicalAnyResolver(parsedPackage)
		for _, file := range parsedPackage.files {
			findings = append(findings, collectParsedFileFindings(parsed.fset, resolver, file)...)
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

// lexicalAnyResolver captures package- and file-block declarations that can
// shadow the universe `any` alias before per-file statement scopes are walked.
type lexicalAnyResolver struct {
	fileScopes             map[*ast.File]lexicalFileScope
	packageScopeShadowsAny bool
}

type lexicalFileScope struct {
	importScopeShadowsAny bool
}

func newParsedPackageLexicalAnyResolver(pkg parsedPackage) lexicalAnyResolver {
	files := make([]*ast.File, 0, len(pkg.files))
	for _, file := range pkg.files {
		files = append(files, file.syntax)
	}
	return newLexicalAnyResolver(files)
}

// newLexicalAnyResolver scans whole-package declarations up front because
// package-block names are in scope regardless of source file or declaration order.
func newLexicalAnyResolver(files []*ast.File) lexicalAnyResolver {
	resolver := lexicalAnyResolver{
		fileScopes: make(map[*ast.File]lexicalFileScope, len(files)),
	}

	for _, file := range files {
		fileScope := scanLexicalFileScope(file)
		resolver.fileScopes[file] = fileScope
		if fileDeclaresPackageAny(file) {
			resolver.packageScopeShadowsAny = true
		}
	}

	return resolver
}

func scanLexicalFileScope(file *ast.File) lexicalFileScope {
	if file == nil {
		return lexicalFileScope{}
	}

	return lexicalFileScope{
		importScopeShadowsAny: fileImportsAny(file),
	}
}

func fileImportsAny(file *ast.File) bool {
	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}

		isImportDecl := genDecl.Tok == token.IMPORT
		if !isImportDecl {
			continue
		}

		if importDeclaresAny(genDecl) {
			return true
		}
	}
	return false
}

func importDeclaresAny(decl *ast.GenDecl) bool {
	for _, spec := range decl.Specs {
		importSpec, ok := spec.(*ast.ImportSpec)
		if !ok {
			continue
		}
		if identDeclaresAny(importSpec.Name) {
			return true
		}
	}
	return false
}

func fileDeclaresPackageAny(file *ast.File) bool {
	if file == nil {
		return false
	}

	for _, decl := range file.Decls {
		if topLevelDeclaresAny(decl) {
			return true
		}
	}
	return false
}

func topLevelDeclaresAny(decl ast.Decl) bool {
	switch node := decl.(type) {
	case *ast.FuncDecl:
		isPackageFunc := node.Recv == nil
		if !isPackageFunc {
			return false
		}
		return identDeclaresAny(node.Name)
	case *ast.GenDecl:
		return genDeclDeclaresAny(node)
	default:
		return false
	}
}

func genDeclDeclaresAny(decl *ast.GenDecl) bool {
	switch decl.Tok {
	case token.CONST, token.VAR:
		return valueSpecsDeclareAny(decl.Specs)
	case token.TYPE:
		return typeSpecsDeclareAny(decl.Specs)
	default:
		return false
	}
}

func valueSpecsDeclareAny(specs []ast.Spec) bool {
	for _, spec := range specs {
		valueSpec, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		if identListDeclaresAny(valueSpec.Names) {
			return true
		}
	}
	return false
}

func typeSpecsDeclareAny(specs []ast.Spec) bool {
	for _, spec := range specs {
		typeSpec, ok := spec.(*ast.TypeSpec)
		if !ok {
			continue
		}
		if identDeclaresAny(typeSpec.Name) {
			return true
		}
	}
	return false
}

func identListDeclaresAny(idents []*ast.Ident) bool {
	for _, ident := range idents {
		if identDeclaresAny(ident) {
			return true
		}
	}
	return false
}

func identDeclaresAny(ident *ast.Ident) bool {
	if ident == nil {
		return false
	}

	isAnyName := ident.Name == anyName
	return isAnyName
}

func collectParsedPackages(
	rootPath, baseAbs string,
	excludeGlobs compiledExcludeGlobs,
	buildCtx *build.Context,
) (parsedPackageCollection, error) {
	fset := token.NewFileSet()
	grouped := make(map[parsedPackageKey]*parsedPackage)
	if buildCtx == nil {
		buildCtx = currentBuildContext()
	}

	err := walkParsedFiles(rootPath, baseAbs, excludeGlobs, fset, buildCtx, func(file parsedGoFile) error {
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
	excludeGlobs compiledExcludeGlobs,
	fset *token.FileSet,
	buildCtx *build.Context,
	visit func(parsedGoFile) error,
) error {
	return filepath.WalkDir(rootPath, func(path string, entry fs.DirEntry, walkErr error) error {
		parsedFile, keep, err := parseRootFile(fset, path, entry, walkErr, baseAbs, excludeGlobs, buildCtx)
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
	excludeGlobs compiledExcludeGlobs,
	buildCtx *build.Context,
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
	if shouldExclude(relPath, excludeGlobs) {
		return parsedGoFile{}, false, nil
	}
	keepForBuild, err := buildCtx.MatchFile(filepath.Dir(path), filepath.Base(path))
	if err != nil {
		return parsedGoFile{}, false, fmt.Errorf("match build constraints for %s: %w", relPath, err)
	}
	if !keepForBuild {
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

func currentBuildContext() *build.Context {
	ctx := build.Default
	if goos := strings.TrimSpace(os.Getenv(goosEnvVar)); goos != "" {
		ctx.GOOS = goos
	}
	if goarch := strings.TrimSpace(os.Getenv(goarchEnvVar)); goarch != "" {
		ctx.GOARCH = goarch
	}
	switch strings.TrimSpace(os.Getenv(cgoEnabledEnvVar)) {
	case "0":
		ctx.CgoEnabled = false
	case "1":
		ctx.CgoEnabled = true
	}
	ctx.BuildTags = buildTagsFromGOFLAGS(os.Getenv(goflagsEnvVar))
	return &ctx
}

func buildContextCacheKey(buildCtx *build.Context) string {
	if buildCtx == nil {
		return ""
	}

	tags := append([]string(nil), buildCtx.BuildTags...)
	sort.Strings(tags)

	cgoEnabled := "0"
	if buildCtx.CgoEnabled {
		cgoEnabled = "1"
	}

	return strings.Join([]string{
		strings.TrimSpace(buildCtx.GOOS),
		strings.TrimSpace(buildCtx.GOARCH),
		cgoEnabled,
		strings.Join(tags, ","),
	}, "\n")
}

// GOFLAGS uses quoted field splitting, and -tags values use the same parsing
// rules as the go command's build tags flag.
func buildTagsFromGOFLAGS(raw string) []string {
	flags, err := splitQuotedFields(raw)
	if err != nil {
		slog.Debug("anyguard ignoring malformed GOFLAGS while computing build tags", logAttrError, err)
		return nil
	}

	var tags []string
	for _, flag := range flags {
		name, value, hasValue := splitFlagValue(flag)
		if !hasValue {
			continue
		}
		if name == "-tags" || name == "--tags" {
			tags = parseBuildTagsFlagValue(value)
		}
	}
	return tags
}

func splitFlagValue(flag string) (string, string, bool) {
	index := strings.Index(flag, "=")
	if index <= 0 {
		return flag, "", false
	}
	return flag[:index], flag[index+1:], true
}

func parseBuildTagsFlagValue(value string) []string {
	if strings.Contains(value, " ") || strings.Contains(value, "'") {
		tags, err := splitQuotedFields(value)
		if err != nil {
			slog.Debug("anyguard ignoring malformed -tags value while computing build tags", "value", value, logAttrError, err)
			return nil
		}
		return normalizeBuildTags(tags)
	}
	return normalizeBuildTags(strings.Split(value, ","))
}

func normalizeBuildTags(tags []string) []string {
	normalized := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if _, exists := seen[tag]; exists {
			continue
		}
		seen[tag] = struct{}{}
		normalized = append(normalized, tag)
	}
	return normalized
}

func splitQuotedFields(value string) ([]string, error) {
	fields := make([]string, 0)
	for value != "" {
		value = trimLeadingSpaceBytes(value)
		if value == "" {
			break
		}

		field, rest, err := nextQuotedField(value)
		if err != nil {
			return nil, err
		}
		fields = append(fields, field)
		value = rest
	}
	return fields, nil
}

func trimLeadingSpaceBytes(value string) string {
	for value != "" && isSpaceByte(value[0]) {
		value = value[1:]
	}
	return value
}

func nextQuotedField(value string) (string, string, error) {
	if value[0] == '"' || value[0] == '\'' {
		return consumeQuotedField(value[0], value[1:])
	}

	index := firstSpaceByte(value)
	return value[:index], value[index:], nil
}

func consumeQuotedField(quote byte, value string) (string, string, error) {
	index := strings.IndexByte(value, quote)
	if index < 0 {
		return "", "", fmt.Errorf("unterminated %c string", quote)
	}
	return value[:index], value[index+1:], nil
}

func firstSpaceByte(value string) int {
	for index := 0; index < len(value); index++ {
		if isSpaceByte(value[index]) {
			return index
		}
	}
	return len(value)
}

func isSpaceByte(value byte) bool {
	return value == ' ' || value == '\t' || value == '\n' || value == '\r'
}

func newParsedPackageKey(file parsedGoFile) parsedPackageKey {
	return parsedPackageKey{
		dir:  filepath.Dir(file.relPath),
		name: file.syntax.Name.Name,
	}
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
			errDuplicateSelector,
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
	if normalized.Line == 0 && normalized.Column == 0 {
		return normalized, nil
	}
	if normalized.Line <= 0 || normalized.Column <= 0 {
		return AnyAllowlistSelector{}, fmt.Errorf(
			"any allowlist entry %d selector line and column must both be positive when either is set",
			index,
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

func buildAllowlistIndex(identities []FindingIdentity) anyAllowlistIndex {
	index := anyAllowlistIndex{
		allowed: make(map[FindingIdentity]struct{}, len(identities)),
	}

	for _, identity := range identities {
		index.allowed[identity] = struct{}{}
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

func collectParsedFileFindings(fset *token.FileSet, resolver lexicalAnyResolver, file parsedGoFile) []collectedFinding {
	nolintLines := collectNolintLines(file.syntax, fset)
	uses := collectAnyUsages(file.relPath, file.syntax, resolver)
	if len(uses) == 0 {
		return nil
	}

	lines := strings.Split(string(file.content), "\n")
	findings := make([]collectedFinding, 0, len(uses))
	for _, usage := range uses {
		pos := fset.Position(usage.pos)
		identity := usage.identity.withPosition(pos.Line, pos.Column)
		findings = append(findings, collectedFinding{
			identity:           identity,
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
	legacyMatches := make(map[FindingIdentity][]FindingIdentity, len(findings))
	for _, finding := range findings {
		available[finding.identity] = struct{}{}
		legacyKey := finding.identity.withoutPosition()
		legacyMatches[legacyKey] = append(legacyMatches[legacyKey], finding.identity)
	}

	resolved := make([]FindingIdentity, 0, len(allowlist.Entries))
	resolvedEntries := make(map[FindingIdentity]int, len(allowlist.Entries))
	for i, entry := range allowlist.Entries {
		identity, err := resolveAllowlistSelector(i, *entry.Selector, available, legacyMatches)
		if err != nil {
			return anyAllowlistIndex{}, err
		}
		if prev, exists := resolvedEntries[identity]; exists {
			return anyAllowlistIndex{}, fmt.Errorf(
				errDuplicateSelector,
				prev,
				i,
				formatFindingIdentity(identity),
			)
		}
		resolvedEntries[identity] = i
		resolved = append(resolved, identity)
	}

	return buildAllowlistIndex(resolved), nil
}

func resolveAllowlistSelector(
	index int,
	selector AnyAllowlistSelector,
	available map[FindingIdentity]struct{},
	legacyMatches map[FindingIdentity][]FindingIdentity,
) (FindingIdentity, error) {
	identity := selector.identity()
	if selector.hasPosition() {
		if _, ok := available[identity]; ok {
			return identity, nil
		}
		return FindingIdentity{}, fmt.Errorf(
			errUnresolvedSelector,
			index,
			formatFindingIdentity(identity),
		)
	}

	matches := legacyMatches[identity]
	switch len(matches) {
	case 0:
		return FindingIdentity{}, fmt.Errorf(
			errUnresolvedSelector,
			index,
			formatFindingIdentity(identity),
		)
	case 1:
		return matches[0], nil
	default:
		return FindingIdentity{}, fmt.Errorf(
			"any allowlist entry %d selector %s is ambiguous and matches %d findings: %s; add line and column",
			index,
			formatFindingIdentity(identity),
			len(matches),
			formatFindingIdentities(matches),
		)
	}
}

func (selector AnyAllowlistSelector) identity() FindingIdentity {
	return FindingIdentity{
		File:     selector.Path,
		Owner:    selector.Owner,
		Category: selector.Category,
		Line:     selector.Line,
		Column:   selector.Column,
	}
}

func formatFindingIdentity(identity FindingIdentity) string {
	if identity.hasPosition() {
		return fmt.Sprintf(
			"{path=%q owner=%q category=%q line=%d column=%d}",
			identity.File,
			identity.Owner,
			identity.Category,
			identity.Line,
			identity.Column,
		)
	}
	return fmt.Sprintf("{path=%q owner=%q category=%q}", identity.File, identity.Owner, identity.Category)
}

func formatFindingIdentities(identities []FindingIdentity) string {
	parts := make([]string, 0, len(identities))
	for _, identity := range identities {
		parts = append(parts, formatFindingIdentity(identity))
	}
	return strings.Join(parts, ", ")
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
	file     string
	resolver lexicalAnyResolver
	scopes   []anyLexicalScope
	syntax   *ast.File
	usages   []anyUsage
}

type anyLexicalScope struct {
	shadowsAny bool
}

func collectAnyUsages(relPath string, file *ast.File, resolver lexicalAnyResolver) []anyUsage {
	collector := anyUsageCollector{
		file:     normalizePath(relPath),
		resolver: resolver,
		syntax:   file,
		usages:   make([]anyUsage, 0),
	}
	collector.inspectFile(file)
	return collector.usages
}

func collectFileAnyUsages(relPath string, file *ast.File) []anyUsage {
	resolver := newLexicalAnyResolver([]*ast.File{file})
	return collectAnyUsages(relPath, file, resolver)
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
		collector.inspectFuncDecl(node, owner)
	}
}

func (collector *anyUsageCollector) inspectFuncDecl(decl *ast.FuncDecl, owner string) {
	if decl == nil {
		return
	}

	collector.openScope()
	defer collector.closeScope()

	collector.declareReceiverTypeParams(decl.Recv)
	if decl.Type != nil {
		collector.declareTypeParams(decl.Type.TypeParams)
	}
	collector.inspectReceiverList(decl.Recv, owner)
	collector.inspectFuncSignature(decl.Type, owner)
	collector.declareFieldNames(decl.Recv)
	if decl.Type != nil {
		collector.declareFieldNames(decl.Type.Params)
		collector.declareFieldNames(decl.Type.Results)
	}
	collector.inspectNode(decl.Body, owner)
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
		collector.inspectTypeSpec(node.Name.Name, node)
	case *ast.ValueSpec:
		owner := valueSpecOwner(node)
		collector.visitPredeclaredAnySlot(anyCategoryValueSpecType, owner, node.Type)
		collector.inspectExprs(node.Values, owner)
	}
}

func (collector *anyUsageCollector) inspectLocalSpec(spec ast.Spec, owner string) {
	switch node := spec.(type) {
	case *ast.TypeSpec:
		collector.declareAnyInCurrentScopeFromIdent(node.Name)
		collector.inspectTypeSpec(owner, node)
	case *ast.ValueSpec:
		collector.visitPredeclaredAnySlot(anyCategoryValueSpecType, owner, node.Type)
		collector.inspectExprs(node.Values, owner)
		collector.declareAnyInCurrentScopeFromIdents(node.Names)
	}
}

func (collector *anyUsageCollector) inspectTypeSpec(owner string, spec *ast.TypeSpec) {
	if spec == nil {
		return
	}

	collector.openScope()
	defer collector.closeScope()

	// Type parameters are visible in the type expression but not after the TypeSpec.
	collector.declareTypeParams(spec.TypeParams)
	collector.visitPredeclaredAnySlot(anyCategoryTypeSpecType, owner, spec.Type)
}

func (collector *anyUsageCollector) inspectFuncSignature(funcType *ast.FuncType, owner string) {
	if funcType == nil {
		return
	}
	collector.inspectFieldList(funcType.Params, owner)
	collector.inspectFieldList(funcType.Results, owner)
}

func (collector *anyUsageCollector) inspectFuncLit(funcLit *ast.FuncLit, owner string) {
	if funcLit == nil {
		return
	}

	collector.openScope()
	defer collector.closeScope()

	collector.inspectFuncSignature(funcLit.Type, owner)
	if funcLit.Type != nil {
		collector.declareFieldNames(funcLit.Type.Params)
		collector.declareFieldNames(funcLit.Type.Results)
	}
	collector.inspectNode(funcLit.Body, owner)
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

func (collector *anyUsageCollector) declareReceiverTypeParams(receivers *ast.FieldList) {
	if receivers == nil {
		return
	}

	hasReceiver := len(receivers.List) > 0
	if !hasReceiver {
		return
	}

	receiverType := receivers.List[0].Type
	if starExpr, ok := receiverType.(*ast.StarExpr); ok {
		receiverType = starExpr.X
	}

	// Receiver type arguments declare method receiver type parameters in Go.
	switch expr := receiverType.(type) {
	case *ast.IndexExpr:
		collector.declareAnyInCurrentScopeFromExpr(expr.Index)
	case *ast.IndexListExpr:
		for _, index := range expr.Indices {
			collector.declareAnyInCurrentScopeFromExpr(index)
		}
	}
}

func (collector *anyUsageCollector) declareAnyInCurrentScopeFromExpr(expr ast.Expr) {
	ident, ok := expr.(*ast.Ident)
	if ok {
		collector.declareAnyInCurrentScopeFromIdent(ident)
	}
}

func (collector *anyUsageCollector) declareFieldNames(fields *ast.FieldList) {
	if fields == nil {
		return
	}
	for _, field := range fields.List {
		if field == nil {
			continue
		}
		collector.declareAnyInCurrentScopeFromIdents(field.Names)
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
		collector.visitPredeclaredAnySlot(anyCategoryFieldType, owner, field.Type)
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

func (collector *anyUsageCollector) openScope() {
	collector.scopes = append(collector.scopes, anyLexicalScope{})
}

func (collector *anyUsageCollector) closeScope() {
	if len(collector.scopes) == 0 {
		return
	}
	collector.scopes = collector.scopes[:len(collector.scopes)-1]
}

func (collector *anyUsageCollector) declareAnyInCurrentScope() {
	if len(collector.scopes) == 0 {
		return
	}

	current := &collector.scopes[len(collector.scopes)-1]
	current.shadowsAny = true
}

func (collector *anyUsageCollector) declareAnyInCurrentScopeFromIdents(idents []*ast.Ident) {
	if identListDeclaresAny(idents) {
		collector.declareAnyInCurrentScope()
	}
}

func (collector *anyUsageCollector) declareAnyInCurrentScopeFromIdent(ident *ast.Ident) {
	if identDeclaresAny(ident) {
		collector.declareAnyInCurrentScope()
	}
}

func (collector *anyUsageCollector) declareTypeParams(typeParams *ast.FieldList) {
	if typeParams == nil {
		return
	}
	for _, field := range typeParams.List {
		if field == nil {
			continue
		}
		collector.declareAnyInCurrentScopeFromIdents(field.Names)
	}
}

func (collector *anyUsageCollector) visitPredeclaredAnySlot(category anyUsageCategory, owner string, expr ast.Expr) {
	if expr == nil {
		return
	}
	collector.recordPredeclaredAnyUsage(category, owner, expr)
	collector.inspectNode(expr, owner)
}

func (collector *anyUsageCollector) visitCompositeTypeAnySlot(category anyUsageCategory, owner string, expr ast.Expr) {
	if expr == nil {
		return
	}
	collector.recordPredeclaredAnyUsage(category, owner, expr)
	collector.inspectNode(expr, owner)
}

func (collector *anyUsageCollector) recordPredeclaredAnyUsage(category anyUsageCategory, owner string, expr ast.Expr) {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return
	}

	resolvesToUniverseAny := collector.resolvesToUniverseAny(ident)
	if !resolvesToUniverseAny {
		return
	}

	collector.usages = append(collector.usages, anyUsage{
		identity: newFindingIdentity(collector.file, owner, category, 0, 0),
		pos:      ident.Pos(),
	})
}

func (collector *anyUsageCollector) resolvesToUniverseAny(ident *ast.Ident) bool {
	if ident == nil || ident.Name != anyName {
		return false
	}
	if collector.resolver.packageScopeShadowsAny {
		return false
	}
	if collector.fileScopeShadowsAny() {
		return false
	}
	return !collector.localScopeShadowsAny()
}

func (collector *anyUsageCollector) fileScopeShadowsAny() bool {
	fileScope := collector.resolver.fileScopes[collector.syntax]
	return fileScope.importScopeShadowsAny
}

func (collector *anyUsageCollector) localScopeShadowsAny() bool {
	for index := len(collector.scopes) - 1; index >= 0; index-- {
		if collector.scopes[index].shadowsAny {
			return true
		}
	}
	return false
}

func newFindingIdentity(relPath, owner string, category anyUsageCategory, line, column int) FindingIdentity {
	return FindingIdentity{
		File:     relPath,
		Owner:    owner,
		Category: string(category),
		Line:     line,
		Column:   column,
	}
}

func (selector AnyAllowlistSelector) hasPosition() bool {
	return selector.Line > 0 && selector.Column > 0
}

func (identity FindingIdentity) hasPosition() bool {
	return identity.Line > 0 && identity.Column > 0
}

func (identity FindingIdentity) withPosition(line, column int) FindingIdentity {
	identity.Line = line
	identity.Column = column
	return identity
}

func (identity FindingIdentity) withoutPosition() FindingIdentity {
	identity.Line = 0
	identity.Column = 0
	return identity
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
		collector.inspectFuncSignature(typed, owner)
	case *ast.FuncLit:
		collector.inspectFuncLit(typed, owner)
	case *ast.StructType:
		collector.inspectFieldList(typed.Fields, owner)
	case *ast.InterfaceType:
		collector.inspectFieldList(typed.Methods, owner)
	case *ast.ArrayType:
		collector.inspectNode(typed.Len, owner)
		collector.visitCompositeTypeAnySlot(anyCategoryArrayTypeElt, owner, typed.Elt)
	case *ast.MapType:
		collector.visitCompositeTypeAnySlot(anyCategoryMapTypeKey, owner, typed.Key)
		collector.visitCompositeTypeAnySlot(anyCategoryMapTypeValue, owner, typed.Value)
	case *ast.ChanType:
		collector.visitCompositeTypeAnySlot(anyCategoryChanTypeValue, owner, typed.Value)
	case *ast.StarExpr:
		collector.visitCompositeTypeAnySlot(anyCategoryStarExprX, owner, typed.X)
	case *ast.Ellipsis:
		collector.visitCompositeTypeAnySlot(anyCategoryEllipsisElt, owner, typed.Elt)
	default:
		return false
	}
	return true
}

func (collector *anyUsageCollector) inspectExprNode(node ast.Node, owner string) bool {
	switch typed := node.(type) {
	case *ast.CallExpr:
		collector.visitPredeclaredAnySlot(anyCategoryCallExprFun, owner, typed.Fun)
		collector.inspectExprs(typed.Args, owner)
	case *ast.TypeAssertExpr:
		collector.inspectNode(typed.X, owner)
		collector.visitPredeclaredAnySlot(anyCategoryTypeAssertType, owner, typed.Type)
	case *ast.IndexExpr:
		collector.inspectNode(typed.X, owner)
		collector.visitPredeclaredAnySlot(anyCategoryIndexExprIndex, owner, typed.Index)
	case *ast.IndexListExpr:
		collector.inspectNode(typed.X, owner)
		for _, index := range typed.Indices {
			collector.visitPredeclaredAnySlot(anyCategoryIndexListIndex, owner, index)
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
		collector.openScope()
		defer collector.closeScope()

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
		collector.inspectAssignStmt(typed, owner)
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

func (collector *anyUsageCollector) inspectAssignStmt(stmt *ast.AssignStmt, owner string) {
	if stmt == nil {
		return
	}

	if stmt.Tok == token.DEFINE {
		// Short variable declarations resolve their RHS before new LHS names exist.
		collector.inspectExprs(stmt.Rhs, owner)
		collector.inspectShortVarDeclLHS(stmt.Lhs, owner)
		collector.declareShortVarNames(stmt.Lhs)
		return
	}

	collector.inspectExprs(stmt.Lhs, owner)
	collector.inspectExprs(stmt.Rhs, owner)
}

func (collector *anyUsageCollector) inspectShortVarDeclLHS(exprs []ast.Expr, owner string) {
	for _, expr := range exprs {
		if _, ok := expr.(*ast.Ident); ok {
			continue
		}
		collector.inspectNode(expr, owner)
	}
}

func (collector *anyUsageCollector) declareShortVarNames(exprs []ast.Expr) {
	for _, expr := range exprs {
		ident, ok := expr.(*ast.Ident)
		if !ok {
			continue
		}
		collector.declareAnyInCurrentScopeFromIdent(ident)
	}
}

func (collector *anyUsageCollector) inspectControlStmtNode(node ast.Node, owner string) bool {
	switch typed := node.(type) {
	case *ast.IfStmt:
		collector.openScope()
		defer collector.closeScope()

		collector.inspectNode(typed.Init, owner)
		collector.inspectNode(typed.Cond, owner)
		collector.inspectNode(typed.Body, owner)
		collector.inspectNode(typed.Else, owner)
	case *ast.SwitchStmt:
		collector.openScope()
		defer collector.closeScope()

		collector.inspectNode(typed.Init, owner)
		collector.inspectNode(typed.Tag, owner)
		collector.inspectNode(typed.Body, owner)
	case *ast.TypeSwitchStmt:
		collector.openScope()
		defer collector.closeScope()

		collector.inspectNode(typed.Init, owner)
		collector.inspectNode(typed.Assign, owner)
		collector.inspectTypeSwitchBody(typed.Body, owner)
	case *ast.CaseClause:
		collector.inspectExprs(typed.List, owner)
		collector.openScope()
		defer collector.closeScope()

		collector.inspectStmts(typed.Body, owner)
	case *ast.CommClause:
		collector.openScope()
		defer collector.closeScope()

		collector.inspectNode(typed.Comm, owner)
		collector.inspectStmts(typed.Body, owner)
	case *ast.SelectStmt:
		collector.inspectNode(typed.Body, owner)
	case *ast.ForStmt:
		collector.openScope()
		defer collector.closeScope()

		collector.inspectNode(typed.Init, owner)
		collector.inspectNode(typed.Cond, owner)
		collector.inspectNode(typed.Post, owner)
		collector.inspectNode(typed.Body, owner)
	case *ast.RangeStmt:
		collector.openScope()
		defer collector.closeScope()

		collector.inspectRangeStmt(typed, owner)
		collector.inspectNode(typed.Body, owner)
	default:
		return false
	}
	return true
}

func (collector *anyUsageCollector) inspectRangeStmt(stmt *ast.RangeStmt, owner string) {
	if stmt == nil {
		return
	}

	collector.inspectNode(stmt.X, owner)
	if stmt.Tok == token.DEFINE {
		collector.inspectRangeDefineLHS(stmt, owner)
		collector.declareRangeNames(stmt)
		return
	}

	collector.inspectNode(stmt.Key, owner)
	collector.inspectNode(stmt.Value, owner)
}

func (collector *anyUsageCollector) inspectRangeDefineLHS(stmt *ast.RangeStmt, owner string) {
	if stmt.Key != nil {
		collector.inspectShortVarDeclLHS([]ast.Expr{stmt.Key}, owner)
	}
	if stmt.Value != nil {
		collector.inspectShortVarDeclLHS([]ast.Expr{stmt.Value}, owner)
	}
}

func (collector *anyUsageCollector) declareRangeNames(stmt *ast.RangeStmt) {
	if stmt.Key != nil {
		collector.declareShortVarNames([]ast.Expr{stmt.Key})
	}
	if stmt.Value != nil {
		collector.declareShortVarNames([]ast.Expr{stmt.Value})
	}
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
		collector.openScope()
		collector.inspectStmts(clause.Body, owner)
		collector.closeScope()
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

// compiledExcludeGlobs keeps exclude patterns in allowlist order while moving
// regex compilation out of per-file filtering.
type compiledExcludeGlobs struct {
	matchers []compiledGlobMatcher
}

type compiledGlobMatcher struct {
	pattern string
	regex   *regexp.Regexp
}

// compileExcludeGlobs reports the first compilation failure with its YAML list
// index so invalid configuration fails closed deterministically.
func compileExcludeGlobs(globs []string) (compiledExcludeGlobs, error) {
	matchers := make([]compiledGlobMatcher, 0, len(globs))
	for i, glob := range globs {
		normalizedGlob := normalizePath(glob)
		if normalizedGlob == "" {
			continue
		}

		matcher, err := compileGlobMatcher(normalizedGlob)
		if err != nil {
			return compiledExcludeGlobs{}, fmt.Errorf("compile exclude_globs[%d]: %w", i, err)
		}

		matchers = append(matchers, matcher)
	}
	return compiledExcludeGlobs{matchers: matchers}, nil
}

func shouldExclude(relPath string, globs compiledExcludeGlobs) bool {
	return globs.matches(relPath)
}

func (globs compiledExcludeGlobs) matches(relPath string) bool {
	normalizedPath := normalizePath(relPath)
	for _, matcher := range globs.matchers {
		if matcher.matchesNormalizedPath(normalizedPath) {
			return true
		}
	}
	return false
}

func matchGlob(pattern, value string) (bool, error) {
	matcher, err := compileGlobMatcher(pattern)
	if err != nil {
		return false, err
	}

	normalizedValue := normalizePath(value)
	return matcher.matchesNormalizedPath(normalizedValue), nil
}

func compileGlobMatcher(pattern string) (compiledGlobMatcher, error) {
	normalizedPattern := normalizePath(pattern)
	expr := globRegexExpression(normalizedPattern)
	regex, err := regexp.Compile(expr)
	if err != nil {
		return compiledGlobMatcher{}, fmt.Errorf("compile glob pattern %q: %w", normalizedPattern, err)
	}
	return compiledGlobMatcher{
		pattern: normalizedPattern,
		regex:   regex,
	}, nil
}

func (matcher compiledGlobMatcher) matchesNormalizedPath(path string) bool {
	return matcher.regex.MatchString(path)
}

func globRegexExpression(pattern string) string {
	// Preserve the historical glob semantics exactly: `*` and `?` stay within a
	// path segment, while `**` may cross slash boundaries.
	escaped := regexp.QuoteMeta(pattern)
	escaped = strings.ReplaceAll(escaped, `\*\*`, anyTokenMarker)
	escaped = strings.ReplaceAll(escaped, `\*`, `[^/]*`)
	escaped = strings.ReplaceAll(escaped, `\?`, `[^/]`)
	escaped = strings.ReplaceAll(escaped, anyTokenMarker, ".*")

	return "^" + escaped + "$"
}
