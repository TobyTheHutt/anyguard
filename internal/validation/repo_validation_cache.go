package validation

import (
	"go/build"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"

	"golang.org/x/sync/singleflight"
)

type repoValidationResult struct {
	index anyAllowlistIndex
}

// repoValidationConfig freezes analyzer-wide inputs that are identical across
// package passes. cacheKey carries the allowlist fingerprint used for exact
// repo validation result reuse.
type repoValidationConfig struct {
	allowlist AnyAllowlist
	buildCtx  *build.Context
	cacheKey  repoValidationCacheKey
	repoRoot  string
	roots     []string
}

type repoValidationConfigCacheKey struct {
	allowlistPath string
	build         string
	repoRoot      string
	roots         string
}

type repoValidationCacheKey struct {
	repoRoot    string
	roots       string
	allowlistID string
	exclude     string
	build       string
}

type anyAllowlistCacheKey struct {
	path string
}

type anyAllowlistCache struct {
	cache processCache[anyAllowlistCacheKey, loadedAnyAllowlist]
}

type repoValidationConfigCache struct {
	cache processCache[repoValidationConfigCacheKey, repoValidationConfig]
}

type repoValidationCache struct {
	cache processCache[repoValidationCacheKey, repoValidationResult]
}

type processCache[K comparable, V any] struct {
	entries sync.Map
	group   singleflight.Group
}

// processCacheEntry stores failures intentionally. Stale allowlist selectors
// should fail closed once, then return the same error on later package passes.
type processCacheEntry[V any] struct {
	value V
	err   error
}

// processRepoValidationCache is intentionally append-only for the lifetime of a
// single anyguard tool process. Analyzer, CLI, and golangci-lint runs are
// short-lived and use a small set of repo/config combinations, so adding
// eviction would increase hot-path synchronization without a practical benefit.
var processRepoValidationCache repoValidationCache

var processRepoValidationConfigCache repoValidationConfigCache

var processAnyAllowlistCache anyAllowlistCache

var repoValidationResultCollector = collectRepoValidationResultWithBuildContext

var repoValidationAllowlistLoader = loadAnyAllowlist

func loadRepoValidationConfig(
	repoRoot string,
	roots []string,
	allowlistPath string,
	buildCtx *build.Context,
) (repoValidationConfig, error) {
	if buildCtx == nil {
		buildCtx = currentBuildContext()
	}

	buildKey := buildContextCacheKey(buildCtx)
	key := newRepoValidationConfigCacheKey(repoRoot, roots, allowlistPath, buildKey)

	return processRepoValidationConfigCache.load(key, func() (repoValidationConfig, error) {
		return collectRepoValidationConfig(key, roots, buildCtx)
	})
}

func collectRepoValidationConfig(
	key repoValidationConfigCacheKey,
	roots []string,
	buildCtx *build.Context,
) (repoValidationConfig, error) {
	loaded, err := loadProcessCachedAnyAllowlist(key.allowlistPath)
	if err != nil {
		return repoValidationConfig{}, err
	}

	normalizedRoots := normalizeConfiguredRoots(roots, key.repoRoot)
	normalizedGlobs := normalizeRepoValidationCacheGlobs(loaded.allowlist.ExcludeGlobs)
	allowlist := loaded.allowlist
	allowlist.ExcludeGlobs = cloneStrings(normalizedGlobs)

	cacheKey := newNormalizedRepoValidationCacheKey(
		key.repoRoot,
		normalizedRoots,
		loaded.fingerprint,
		normalizedGlobs,
		key.build,
	)
	return repoValidationConfig{
		allowlist: allowlist,
		buildCtx:  cloneBuildContext(buildCtx),
		cacheKey:  cacheKey,
		repoRoot:  key.repoRoot,
		roots:     cloneStrings(normalizedRoots),
	}, nil
}

func loadProcessCachedAnyAllowlist(listPath string) (loadedAnyAllowlist, error) {
	key := newAnyAllowlistCacheKey(listPath)
	return processAnyAllowlistCache.load(key, func() (loadedAnyAllowlist, error) {
		return repoValidationAllowlistLoader(key.path)
	})
}

func loadRepoValidationResult(config repoValidationConfig) (repoValidationResult, error) {
	return processRepoValidationCache.load(config.cacheKey, func() (repoValidationResult, error) {
		return repoValidationResultCollector(config.repoRoot, config.roots, config.allowlist, config.buildCtx)
	})
}

func collectRepoValidationResultWithBuildContext(
	repoRoot string,
	roots []string,
	allowlist AnyAllowlist,
	buildCtx *build.Context,
) (repoValidationResult, error) {
	findings, err := collectFindingsWithBuildContext(repoRoot, roots, allowlist.ExcludeGlobs, buildCtx)
	if err != nil {
		return repoValidationResult{}, err
	}

	index, err := resolveAllowlistIndex(allowlist, findings)
	if err != nil {
		return repoValidationResult{}, err
	}

	return repoValidationResult{index: index}, nil
}

func newRepoValidationCacheKey(
	repoRoot string,
	roots []string,
	allowlistFingerprint string,
	excludeGlobs []string,
	buildKey string,
) repoValidationCacheKey {
	cleanRepoRoot := filepath.Clean(repoRoot)
	normalizedRoots := normalizeConfiguredRoots(roots, cleanRepoRoot)
	normalizedGlobs := normalizeRepoValidationCacheGlobs(excludeGlobs)
	return newNormalizedRepoValidationCacheKey(
		cleanRepoRoot,
		normalizedRoots,
		allowlistFingerprint,
		normalizedGlobs,
		buildKey,
	)
}

// newNormalizedRepoValidationCacheKey expects caller-normalized inputs. The
// analyzer path produces them in collectRepoValidationConfig before caching.
func newNormalizedRepoValidationCacheKey(
	repoRoot string,
	normalizedRoots []string,
	allowlistFingerprint string,
	normalizedGlobs []string,
	buildKey string,
) repoValidationCacheKey {
	rootKeyParts := cloneStrings(normalizedRoots)
	sort.Strings(rootKeyParts)

	globKeyParts := cloneStrings(normalizedGlobs)
	sort.Strings(globKeyParts)

	return repoValidationCacheKey{
		repoRoot:    filepath.ToSlash(repoRoot),
		roots:       strings.Join(rootKeyParts, "\n"),
		allowlistID: strings.TrimSpace(allowlistFingerprint),
		exclude:     strings.Join(globKeyParts, "\n"),
		build:       strings.TrimSpace(buildKey),
	}
}

func newRepoValidationConfigCacheKey(
	repoRoot string,
	roots []string,
	allowlistPath string,
	buildKey string,
) repoValidationConfigCacheKey {
	return repoValidationConfigCacheKey{
		allowlistPath: filepath.Clean(allowlistPath),
		build:         strings.TrimSpace(buildKey),
		repoRoot:      filepath.Clean(repoRoot),
		roots:         strings.Join(roots, "\n"),
	}
}

func newAnyAllowlistCacheKey(listPath string) anyAllowlistCacheKey {
	return anyAllowlistCacheKey{
		path: filepath.Clean(listPath),
	}
}

func normalizeRepoValidationCacheGlobs(globs []string) []string {
	normalized := make([]string, 0, len(globs))
	for _, glob := range globs {
		glob = normalizePath(glob)
		if glob == "" {
			continue
		}
		normalized = append(normalized, glob)
	}
	return normalized
}

func cloneBuildContext(buildCtx *build.Context) *build.Context {
	if buildCtx == nil {
		return currentBuildContext()
	}

	cloned := *buildCtx
	cloned.BuildTags = cloneStrings(buildCtx.BuildTags)
	cloned.ToolTags = cloneStrings(buildCtx.ToolTags)
	cloned.ReleaseTags = cloneStrings(buildCtx.ReleaseTags)
	return &cloned
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string(nil), values...)
}

func (cache *anyAllowlistCache) load(
	key anyAllowlistCacheKey,
	collect func() (loadedAnyAllowlist, error),
) (loadedAnyAllowlist, error) {
	return cache.cache.load(key, key.singleflightKey(), collect, "unexpected any allowlist cache value type")
}

func (key anyAllowlistCacheKey) singleflightKey() string {
	return key.path
}

func (cache *repoValidationConfigCache) load(
	key repoValidationConfigCacheKey,
	collect func() (repoValidationConfig, error),
) (repoValidationConfig, error) {
	return cache.cache.load(key, key.singleflightKey(), collect, "unexpected repo validation config cache value type")
}

func (key repoValidationConfigCacheKey) singleflightKey() string {
	return strings.Join([]string{key.repoRoot, key.roots, key.allowlistPath, key.build}, "\x00")
}

func (cache *repoValidationCache) load(
	key repoValidationCacheKey,
	collect func() (repoValidationResult, error),
) (repoValidationResult, error) {
	return cache.cache.load(key, key.singleflightKey(), collect, "unexpected repo validation cache value type")
}

func (cache *processCache[K, V]) load(
	key K,
	singleflightKey string,
	collect func() (V, error),
	typeErr string,
) (V, error) {
	if cached, found := cache.entries.Load(key); found {
		return unpackProcessCacheEntry[V](cached, typeErr)
	}

	value, err, _ := cache.group.Do(singleflightKey, func() (interface{}, error) {
		if cached, found := cache.entries.Load(key); found {
			return cached, nil
		}

		result, resultErr := collect()
		entry := processCacheEntry[V]{
			value: result,
			err:   resultErr,
		}
		cache.entries.Store(key, entry)
		return entry, nil
	})
	if err != nil {
		var zero V
		return zero, err
	}

	return unpackProcessCacheEntry[V](value, typeErr)
}

func unpackProcessCacheEntry[V any](value interface{}, typeErr string) (V, error) {
	entry, ok := value.(processCacheEntry[V])
	if ok {
		return entry.value, entry.err
	}

	valueType := reflect.TypeOf(value)
	if valueType == nil {
		panic(typeErr + ": got <nil>")
	}
	panic(typeErr + ": got " + valueType.String())
}

func (key repoValidationCacheKey) singleflightKey() string {
	return strings.Join([]string{key.repoRoot, key.roots, key.allowlistID, key.exclude, key.build}, "\x00")
}

func resetProcessRepoValidationCacheForTesting() {
	processRepoValidationCache = repoValidationCache{}
	processRepoValidationConfigCache = repoValidationConfigCache{}
	processAnyAllowlistCache = anyAllowlistCache{}
}
