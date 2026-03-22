package validation

import (
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tobythehutt/anyguard/internal/benchtest"
	"golang.org/x/tools/go/analysis"
)

const (
	testCacheRepoName     = "repo"
	testCacheFingerprintA = "fingerprint-a"
	testCacheFingerprintB = "fingerprint-b"
)

func TestNewRepoValidationCacheKeyNormalizesInputs(t *testing.T) {
	base := t.TempDir()
	repoRoot := filepath.Join(base, testCacheRepoName, ".")
	keyA := newRepoValidationCacheKey(
		repoRoot,
		[]string{"./...", filepath.Join(base, testCacheRepoName, testDirPkg, testDirAPI)},
		testCacheFingerprintA,
		[]string{" ./internal/** ", "**/*_test.go"},
	)
	keyB := newRepoValidationCacheKey(
		filepath.Join(base, testCacheRepoName),
		[]string{"pkg/api", "."},
		testCacheFingerprintA,
		[]string{"**/*_test.go", "internal/**"},
	)

	if keyA != keyB {
		t.Fatalf("expected normalized keys to match:\nkeyA=%#v\nkeyB=%#v", keyA, keyB)
	}

	keyC := newRepoValidationCacheKey(
		filepath.Join(base, testCacheRepoName),
		[]string{"pkg/api", "."},
		testCacheFingerprintB,
		[]string{"**/*_test.go", "internal/**"},
	)
	if keyA == keyC {
		t.Fatalf("expected allowlist fingerprint to affect cache key")
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
	)
	keyB := newRepoValidationCacheKey(
		filepath.Join(base, testCacheRepoName),
		[]string{"pkg/api", "."},
		testCacheFingerprintA,
		[]string{"**/*_test.go", "internal/**"},
	)

	want := repoValidationResult{
		index: anyAllowlistIndex{
			allowed: map[FindingIdentity]struct{}{
				newFindingIdentity(testPayloadPath, testOwnerPayload, anyCategoryMapTypeValue): {},
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

func TestRepoValidationCacheConcurrentAccessCollapsesMisses(t *testing.T) {
	var cache repoValidationCache
	key := newRepoValidationCacheKey(
		filepath.Join(t.TempDir(), testCacheRepoName),
		[]string{DefaultRoots},
		testCacheFingerprintA,
		[]string{"**/*_test.go"},
	)

	want := repoValidationResult{
		index: anyAllowlistIndex{
			allowed: map[FindingIdentity]struct{}{
				newFindingIdentity(testPayloadPath, testOwnerPayload, anyCategoryMapTypeValue): {},
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
		t.Fatalf("expected one representative snapshot, got %d", len(snapshots))
	}

	cfg := &analyzerConfig{
		allowlistPath: fixture.AllowlistRelPath,
		repoRoot:      fixture.Root,
		roots:         DefaultRoots,
	}

	originalCollector := repoValidationResultCollector
	var calls atomic.Int64
	repoValidationResultCollector = func(repoRoot string, roots []string, allowlist AnyAllowlist) (repoValidationResult, error) {
		calls.Add(1)
		return originalCollector(repoRoot, roots, allowlist)
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
