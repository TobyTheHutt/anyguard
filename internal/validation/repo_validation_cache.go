package validation

import (
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"golang.org/x/sync/singleflight"
)

type repoValidationResult struct {
	index anyAllowlistIndex
}

type repoValidationCacheKey struct {
	repoRoot    string
	roots       string
	allowlistID string
	exclude     string
}

type repoValidationCache struct {
	entries sync.Map
	group   singleflight.Group
}

// processRepoValidationCache is intentionally append-only for the lifetime of a
// single anyguard tool process. Analyzer, CLI, and golangci-lint runs are
// short-lived and use a small set of repo/config combinations, so adding
// eviction would increase hot-path synchronization without a practical benefit.
var processRepoValidationCache repoValidationCache

var repoValidationResultCollector = collectRepoValidationResult

func loadRepoValidationResult(
	repoRoot string,
	roots []string,
	allowlist AnyAllowlist,
	allowlistFingerprint string,
) (repoValidationResult, error) {
	key := newRepoValidationCacheKey(repoRoot, roots, allowlistFingerprint, allowlist.ExcludeGlobs)
	return processRepoValidationCache.load(key, func() (repoValidationResult, error) {
		return repoValidationResultCollector(repoRoot, roots, allowlist)
	})
}

func collectRepoValidationResult(repoRoot string, roots []string, allowlist AnyAllowlist) (repoValidationResult, error) {
	findings, err := collectFindings(repoRoot, roots, allowlist.ExcludeGlobs)
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
) repoValidationCacheKey {
	cleanRepoRoot := filepath.Clean(repoRoot)
	normalizedRoots := normalizeConfiguredRoots(roots, cleanRepoRoot)
	sort.Strings(normalizedRoots)

	normalizedGlobs := normalizeRepoValidationCacheGlobs(excludeGlobs)
	sort.Strings(normalizedGlobs)

	return repoValidationCacheKey{
		repoRoot:    filepath.ToSlash(cleanRepoRoot),
		roots:       strings.Join(normalizedRoots, "\n"),
		allowlistID: strings.TrimSpace(allowlistFingerprint),
		exclude:     strings.Join(normalizedGlobs, "\n"),
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

func (cache *repoValidationCache) load(
	key repoValidationCacheKey,
	collect func() (repoValidationResult, error),
) (repoValidationResult, error) {
	if cached, found := cache.entries.Load(key); found {
		cachedResult, ok := cached.(repoValidationResult)
		if ok {
			return cachedResult, nil
		}
	}

	value, err, _ := cache.group.Do(key.singleflightKey(), func() (any, error) {
		if cached, ok := cache.entries.Load(key); ok {
			return cached, nil
		}

		result, collectErr := collect()
		if collectErr != nil {
			return repoValidationResult{}, collectErr
		}

		cache.entries.Store(key, result)
		return result, nil
	})
	if err != nil {
		return repoValidationResult{}, err
	}

	result, ok := value.(repoValidationResult)
	if ok {
		return result, nil
	}
	return repoValidationResult{}, errors.New("unexpected repo validation cache value type")
}

func (key repoValidationCacheKey) singleflightKey() string {
	return strings.Join([]string{key.repoRoot, key.roots, key.allowlistID, key.exclude}, "\x00")
}

func resetProcessRepoValidationCacheForTesting() {
	processRepoValidationCache = repoValidationCache{}
}
