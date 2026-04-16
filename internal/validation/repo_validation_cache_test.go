package validation

import (
	"errors"
	"go/build"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tobythehutt/anyguard/v2/internal/benchtest"
	"golang.org/x/tools/go/analysis"
)

const (
	testCacheRepoName     = "repo"
	testCacheFingerprintA = "fingerprint-a"
	testCacheFingerprintB = "fingerprint-b"
	testProcessCacheKey   = "wrong-type"

	errExpectedRepresentativeSnapshot = "expected one representative snapshot, got %d"
)

func TestNewRepoValidationCacheKeyNormalizesInputs(t *testing.T) {
	base := t.TempDir()
	repoRoot := filepath.Join(base, testCacheRepoName, ".")
	buildKeyA := buildContextCacheKey(testBuildContext(testGOOSLinux, testGOARCHAMD64, testBuildTagCustom, testBuildTagExtra))
	buildKeyB := buildContextCacheKey(testBuildContext(testGOOSLinux, testGOARCHAMD64, testBuildTagExtra, testBuildTagCustom))
	keyA := newRepoValidationCacheKey(
		repoRoot,
		[]string{"./...", filepath.Join(base, testCacheRepoName, testDirPkg, testDirAPI)},
		testCacheFingerprintA,
		[]string{" ./internal/** ", "**/*_test.go"},
		buildKeyA,
	)
	keyB := newRepoValidationCacheKey(
		filepath.Join(base, testCacheRepoName),
		[]string{"pkg/api", "."},
		testCacheFingerprintA,
		[]string{"**/*_test.go", "internal/**"},
		buildKeyB,
	)

	if keyA != keyB {
		t.Fatalf("expected normalized keys to match:\nkeyA=%#v\nkeyB=%#v", keyA, keyB)
	}

	keyC := newRepoValidationCacheKey(
		filepath.Join(base, testCacheRepoName),
		[]string{"pkg/api", "."},
		testCacheFingerprintB,
		[]string{"**/*_test.go", "internal/**"},
		buildKeyA,
	)
	if keyA == keyC {
		t.Fatalf("expected allowlist fingerprint to affect cache key")
	}

	keyD := newRepoValidationCacheKey(
		filepath.Join(base, testCacheRepoName),
		[]string{"pkg/api", "."},
		testCacheFingerprintA,
		[]string{"**/*_test.go", "internal/**"},
		buildContextCacheKey(testBuildContext(testGOOSLinux, testGOARCHAMD64, testBuildTagCustom)),
	)
	if keyA == keyD {
		t.Fatalf("expected build constraints to affect cache key")
	}
}

func TestRepoValidationCacheReusesNormalizedKey(t *testing.T) {
	var cache repoValidationCache
	base := t.TempDir()

	keyA := newRepoValidationCacheKey(
		filepath.Join(base, testCacheRepoName, "."),
		[]string{"./...", filepath.Join(base, testCacheRepoName, testDirPkg, testDirAPI)},
		testCacheFingerprintA,
		[]string{" ./internal/** ", "**/*_test.go"},
		buildContextCacheKey(testBuildContext(testGOOSLinux, testGOARCHAMD64, testBuildTagCustom)),
	)
	keyB := newRepoValidationCacheKey(
		filepath.Join(base, testCacheRepoName),
		[]string{"pkg/api", "."},
		testCacheFingerprintA,
		[]string{"**/*_test.go", "internal/**"},
		buildContextCacheKey(testBuildContext(testGOOSLinux, testGOARCHAMD64, testBuildTagCustom)),
	)

	want := repoValidationResult{
		index: anyAllowlistIndex{
			allowed: map[FindingIdentity]struct{}{
				newFindingIdentity(testPayloadPath, testOwnerPayload, anyCategoryMapTypeValue, testPayloadLine, testPayloadColumn): {},
			},
		},
	}

	var calls atomic.Int64
	collect := func() (repoValidationResult, error) {
		calls.Add(1)
		return want, nil
	}

	gotA, err := cache.load(keyA, collect)
	if err != nil {
		t.Fatalf("load keyA: %v", err)
	}
	gotB, err := cache.load(keyB, collect)
	if err != nil {
		t.Fatalf("load keyB: %v", err)
	}

	if calls.Load() != 1 {
		t.Fatalf("expected one cache miss, got %d", calls.Load())
	}
	if !reflect.DeepEqual(gotA, want) {
		t.Fatalf("unexpected cached result for keyA: got %#v want %#v", gotA, want)
	}
	if !reflect.DeepEqual(gotB, want) {
		t.Fatalf("unexpected cached result for keyB: got %#v want %#v", gotB, want)
	}
}

func TestRepoValidationCacheReusesFailures(t *testing.T) {
	var cache repoValidationCache
	key := newRepoValidationCacheKey(
		filepath.Join(t.TempDir(), testCacheRepoName),
		[]string{DefaultRoots},
		testCacheFingerprintA,
		[]string{"**/*_test.go"},
		buildContextCacheKey(testBuildContext(testGOOSLinux, testGOARCHAMD64, testBuildTagCustom)),
	)

	wantErr := errors.New("stale allowlist selector")
	var calls atomic.Int64
	collect := func() (repoValidationResult, error) {
		calls.Add(1)
		return repoValidationResult{}, wantErr
	}

	gotA, errA := cache.load(key, collect)
	gotB, errB := cache.load(key, collect)

	if calls.Load() != 1 {
		t.Fatalf("expected one failed cache miss, got %d", calls.Load())
	}
	if !errors.Is(errA, wantErr) {
		t.Fatalf("unexpected first cached error: %v", errA)
	}
	if !errors.Is(errB, wantErr) {
		t.Fatalf("unexpected second cached error: %v", errB)
	}
	if !reflect.DeepEqual(gotA, repoValidationResult{}) {
		t.Fatalf("unexpected first failed result: %#v", gotA)
	}
	if !reflect.DeepEqual(gotB, repoValidationResult{}) {
		t.Fatalf("unexpected second failed result: %#v", gotB)
	}
}

func TestLoadRepoValidationConfigNormalizesInputs(t *testing.T) {
	resetProcessRepoValidationCacheForTesting()
	t.Cleanup(resetProcessRepoValidationCacheForTesting)

	repoRoot := t.TempDir()
	allowlistPath := filepath.Join(repoRoot, testAllowlistFile)
	writeRawAllowlist(t, allowlistPath, strings.Join([]string{
		"version: 2",
		"exclude_globs:",
		"  - \" \"",
		"  - \"./pkg//*.go\"",
		"entries: []",
		"",
	}, "\n"))

	config, err := loadRepoValidationConfig(repoRoot, []string{DefaultRoots}, allowlistPath, nil)
	if err != nil {
		t.Fatalf("load repo validation config: %v", err)
	}
	if config.buildCtx == nil {
		t.Fatalf("expected cached build context")
	}
	if config.cacheKey.build == "" {
		t.Fatalf("expected cached build fingerprint")
	}
	if !reflect.DeepEqual(config.roots, []string{"."}) {
		t.Fatalf("unexpected normalized roots: %#v", config.roots)
	}
	const wantExcludeGlob = "pkg/*.go"
	wantExcludeGlobs := []string{wantExcludeGlob}
	if !reflect.DeepEqual(config.allowlist.ExcludeGlobs, wantExcludeGlobs) {
		t.Fatalf("unexpected normalized exclude globs: %#v", config.allowlist.ExcludeGlobs)
	}
	if got, want := len(config.excludeGlobs.matchers), 1; got != want {
		t.Fatalf("unexpected compiled exclude glob count: got %d want %d", got, want)
	}
	if got, want := config.excludeGlobs.matchers[0].pattern, wantExcludeGlob; got != want {
		t.Fatalf("unexpected compiled exclude glob pattern: got %q want %q", got, want)
	}
}

func TestLoadRepoValidationConfigCachesAllowlistFailures(t *testing.T) {
	resetProcessRepoValidationCacheForTesting()
	t.Cleanup(resetProcessRepoValidationCacheForTesting)

	repoRoot := t.TempDir()
	allowlistPath := filepath.Join(repoRoot, testAllowlistFile)
	originalLoader := repoValidationAllowlistLoader
	var allowlistLoads atomic.Int64
	repoValidationAllowlistLoader = func(listPath string) (loadedAnyAllowlist, error) {
		allowlistLoads.Add(1)
		return originalLoader(listPath)
	}
	t.Cleanup(func() {
		repoValidationAllowlistLoader = originalLoader
	})

	for i := 0; i < 2; i++ {
		_, err := loadRepoValidationConfig(repoRoot, []string{DefaultRoots}, allowlistPath, currentBuildContext())
		if err == nil {
			t.Fatalf("expected missing allowlist error on load %d", i+1)
		}
	}
	if allowlistLoads.Load() != 1 {
		t.Fatalf("expected one cached allowlist load failure, got %d", allowlistLoads.Load())
	}
}

func TestProcessCachePanicsOnUnexpectedValueType(t *testing.T) {
	var cache processCache[string, int]
	cache.entries.Store(testProcessCacheKey, "cached string")

	expectPanicContains(t, "unexpected test cache value type: got string", func() {
		result, err := cache.load(testProcessCacheKey, testProcessCacheKey, func() (int, error) {
			return 1, nil
		}, "unexpected test cache value type")
		if err != nil {
			t.Fatalf("unexpected cache error before panic: %v", err)
		}
		_ = result
	})
	expectPanicContains(t, "unexpected nil cache value type: got <nil>", func() {
		result, err := unpackProcessCacheEntry[int](nil, "unexpected nil cache value type")
		if err != nil {
			t.Fatalf("unexpected unpack error before panic: %v", err)
		}
		_ = result
	})
}

func TestRepoValidationCacheConcurrentAccessCollapsesMisses(t *testing.T) {
	var cache repoValidationCache
	key := newRepoValidationCacheKey(
		filepath.Join(t.TempDir(), testCacheRepoName),
		[]string{DefaultRoots},
		testCacheFingerprintA,
		[]string{"**/*_test.go"},
		buildContextCacheKey(testBuildContext(testGOOSLinux, testGOARCHAMD64, testBuildTagCustom)),
	)

	want := repoValidationResult{
		index: anyAllowlistIndex{
			allowed: map[FindingIdentity]struct{}{
				newFindingIdentity(testPayloadPath, testOwnerPayload, anyCategoryMapTypeValue, testPayloadLine, testPayloadColumn): {},
			},
		},
	}

	var calls atomic.Int64
	collect := func() (repoValidationResult, error) {
		calls.Add(1)
		time.Sleep(25 * time.Millisecond)
		return want, nil
	}

	const goroutineCount = 16
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan repoValidationResult, goroutineCount)
	errs := make(chan error, goroutineCount)

	for i := 0; i < goroutineCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start

			got, err := cache.load(key, collect)
			results <- got
			errs <- err
		}()
	}

	close(start)
	wg.Wait()
	close(results)
	close(errs)

	if calls.Load() != 1 {
		t.Fatalf("expected one concurrent cache miss, got %d", calls.Load())
	}

	for err := range errs {
		if err != nil {
			t.Fatalf("unexpected concurrent cache error: %v", err)
		}
	}
	for got := range results {
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("unexpected concurrent cached result: got %#v want %#v", got, want)
		}
	}
}

func TestAnalyzerRunReusesRepoValidationFailureAcrossPasses(t *testing.T) {
	resetProcessRepoValidationCacheForTesting()
	t.Cleanup(resetProcessRepoValidationCacheForTesting)

	fixture := benchtest.CreateSyntheticRepo(t, benchtest.SyntheticRepoConfig{
		PackageCount: 4,
		SafeFiles:    1,
		UsageFiles:   1,
	})
	writeStaleAllowlist(t, fixture.AllowlistPath)

	snapshots := benchtest.LoadPackageSnapshots(t, fixture.Root, []string{fixture.RepresentativePackage})
	if len(snapshots) != 1 {
		t.Fatalf(errExpectedRepresentativeSnapshot, len(snapshots))
	}

	cfg := &analyzerConfig{
		allowlistPath: fixture.AllowlistRelPath,
		repoRoot:      fixture.Root,
		roots:         DefaultRoots,
	}

	originalLoader := repoValidationAllowlistLoader
	var allowlistLoads atomic.Int64
	repoValidationAllowlistLoader = func(listPath string) (loadedAnyAllowlist, error) {
		allowlistLoads.Add(1)
		return originalLoader(listPath)
	}
	t.Cleanup(func() {
		repoValidationAllowlistLoader = originalLoader
	})

	originalCollector := repoValidationResultCollector
	var repoValidationCalls atomic.Int64
	repoValidationResultCollector = func(
		repoRoot string,
		roots []string,
		allowlist AnyAllowlist,
		excludeGlobs compiledExcludeGlobs,
		buildCtx *build.Context,
	) (repoValidationResult, error) {
		repoValidationCalls.Add(1)
		return originalCollector(repoRoot, roots, allowlist, excludeGlobs, buildCtx)
	}
	t.Cleanup(func() {
		repoValidationResultCollector = originalCollector
	})

	snapshot := snapshots[0]
	errorMessages := make([]string, 0, 2)
	for i := 0; i < 2; i++ {
		pass := benchtest.NewPass(snapshot, NewAnalyzer(), nil)
		_, err := cfg.run(pass)
		if err == nil {
			t.Fatalf("expected stale-selector error on analyzer pass %d", i+1)
		}
		errorMessages = append(errorMessages, err.Error())
	}

	if allowlistLoads.Load() != 1 {
		t.Fatalf("expected one allowlist load across failing passes, got %d", allowlistLoads.Load())
	}
	if repoValidationCalls.Load() != 1 {
		t.Fatalf("expected one failed repo-wide validation across repeated passes, got %d", repoValidationCalls.Load())
	}
	if errorMessages[0] != errorMessages[1] {
		t.Fatalf("expected stable cached errors, got %#v", errorMessages)
	}
}

func TestAnalyzerRunReusesRepoValidationCacheAcrossPasses(t *testing.T) {
	resetProcessRepoValidationCacheForTesting()
	t.Cleanup(resetProcessRepoValidationCacheForTesting)

	fixture := benchtest.CreateSyntheticRepo(t, benchtest.SyntheticRepoConfig{
		PackageCount: 4,
		SafeFiles:    1,
		UsageFiles:   1,
	})
	snapshots := benchtest.LoadPackageSnapshots(t, fixture.Root, []string{fixture.RepresentativePackage})
	if len(snapshots) != 1 {
		t.Fatalf(errExpectedRepresentativeSnapshot, len(snapshots))
	}

	cfg := &analyzerConfig{
		allowlistPath: fixture.AllowlistRelPath,
		repoRoot:      fixture.Root,
		roots:         DefaultRoots,
	}

	originalCollector := repoValidationResultCollector
	var calls atomic.Int64
	repoValidationResultCollector = func(
		repoRoot string,
		roots []string,
		allowlist AnyAllowlist,
		excludeGlobs compiledExcludeGlobs,
		buildCtx *build.Context,
	) (repoValidationResult, error) {
		calls.Add(1)
		return originalCollector(repoRoot, roots, allowlist, excludeGlobs, buildCtx)
	}
	t.Cleanup(func() {
		repoValidationResultCollector = originalCollector
	})

	snapshot := snapshots[0]
	diagnosticCounts := make([]int, 0, 2)
	for i := 0; i < 2; i++ {
		diagnosticCount := 0
		pass := benchtest.NewPass(snapshot, NewAnalyzer(), func(analysis.Diagnostic) {
			diagnosticCount++
		})

		if _, err := cfg.run(pass); err != nil {
			t.Fatalf("run analyzer pass %d: %v", i+1, err)
		}
		diagnosticCounts = append(diagnosticCounts, diagnosticCount)
	}

	if calls.Load() != 1 {
		t.Fatalf("expected one repo-wide validation run across repeated analyzer passes, got %d", calls.Load())
	}
	if len(diagnosticCounts) != 2 || diagnosticCounts[0] == 0 {
		t.Fatalf("expected repeated analyzer diagnostics, got %#v", diagnosticCounts)
	}
	if diagnosticCounts[0] != diagnosticCounts[1] {
		t.Fatalf("expected stable repeated analyzer diagnostics, got %#v", diagnosticCounts)
	}
}

func writeStaleAllowlist(t *testing.T, path string) {
	t.Helper()

	lines := []string{
		"version: 2",
		"exclude_globs:",
		"  - \"**/*_test.go\"",
		"entries:",
		"  - selector:",
		"      path: \"pkg/missing.go\"",
		"      owner: \"Missing\"",
		"      category: \"*ast.MapType.Value\"",
		"      line: 1",
		"      column: 1",
		"    description: stale selector",
		"",
	}
	content := strings.Join(lines, "\n")
	err := os.WriteFile(path, []byte(content), 0o600)
	if err != nil {
		t.Fatalf("write stale allowlist: %v", err)
	}
}

func writeRawAllowlist(t *testing.T, path, content string) {
	t.Helper()

	err := os.WriteFile(path, []byte(content), 0o600)
	if err != nil {
		t.Fatalf("write allowlist: %v", err)
	}
}

func expectPanicContains(t *testing.T, want string, run func()) {
	t.Helper()

	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatalf("expected panic containing %q", want)
		}

		message, ok := recovered.(string)
		if !ok {
			t.Fatalf("expected string panic, got %#v", recovered)
		}
		if !strings.Contains(message, want) {
			t.Fatalf("unexpected panic: got %q want substring %q", message, want)
		}
	}()

	run()
}
