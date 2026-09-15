package gitignore

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockAPI points API at an httptest server serving the given per-language
// bodies and returns a restore func.
func mockAPI(t *testing.T, bodies map[string]string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		body, ok := bodies[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if _, err := fmt.Fprint(w, body); err != nil {
			t.Errorf("failed to write response: %v", err)
		}
	}))
	original := API
	API = server.URL
	t.Cleanup(func() {
		API = original
		server.Close()
	})
}

// unreachableAPI points API at a closed server, so any network access fails
// deterministically (connection refused) instead of hitting the real API.
func unreachableAPI(t *testing.T) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	original := API
	API = server.URL
	server.Close() // refuse all connections from here on
	t.Cleanup(func() { API = original })
}

// apiBody wraps a template section in gitignore.io's standard response
// wrapper, exactly as the API returns it.
func apiBody(section string) string {
	return "# Created by https://www.toptal.com/developers/gitignore/api/go\n" +
		"# Edit at https://www.toptal.com/developers/gitignore?templates=go\n\n" +
		section + "\n" +
		"# End of https://www.toptal.com/developers/gitignore/api/go\n"
}

// useCacheDir points CacheDir at a fresh temp directory and restores the
// original tunables after the test.
func useCacheDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	originalDir, originalTTL, originalUser := CacheDir, CacheTTL, userCacheDir
	CacheDir = dir
	t.Cleanup(func() {
		CacheDir = originalDir
		CacheTTL = originalTTL
		userCacheDir = originalUser
	})
	return dir
}

func TestStripWrapperRemovesPrologueAndTrailer(t *testing.T) {
	body := apiBody("### Go ###\n*.exe\n*.test")

	want := "### Go ###\n*.exe\n*.test"
	if got := stripWrapper(body); got != want {
		t.Errorf("stripWrapper() =\n%q\nwant\n%q", got, want)
	}
}

func TestStripWrapperKeepsInnerCommentLines(t *testing.T) {
	// "# Created by"-like lines inside a template must survive; only the
	// prologue before "# Edit at " and everything from "# End of " is cut.
	body := "# Created by https://www.toptal.com/developers/gitignore/api/x\n" +
		"# Edit at https://www.toptal.com/developers/gitignore?templates=x\n\n" +
		"# Created by sometool\n*.log\n" +
		"# End of https://www.toptal.com/developers/gitignore/api/x\n"

	want := "# Created by sometool\n*.log"
	if got := stripWrapper(body); got != want {
		t.Errorf("stripWrapper() =\n%q\nwant\n%q", got, want)
	}
}

func TestStripWrapperWithoutMarkers(t *testing.T) {
	// Plain bodies without the wrapper are returned unchanged.
	for _, body := range []string{"*.exe\n*.test", "", "# just a comment"} {
		if got := stripWrapper(body); got != body {
			t.Errorf("stripWrapper(%q) = %q, want unchanged", body, got)
		}
	}
}

func TestMergePreservesSelectionOrder(t *testing.T) {
	useCacheDir(t)
	mockAPI(t, map[string]string{
		"go":     apiBody("### Go ###\n*.exe"),
		"python": apiBody("### Python ###\n*.pyc"),
		"rust":   apiBody("### Rust ###\ntarget/"),
	})

	got, err := Merge([]string{"rust", "go", "python"})
	if err != nil {
		t.Fatalf("Merge() error = %v", err)
	}

	goIdx := strings.Index(got, "### Go ###")
	pyIdx := strings.Index(got, "### Python ###")
	rustIdx := strings.Index(got, "### Rust ###")
	if goIdx < 0 || pyIdx < 0 || rustIdx < 0 {
		t.Fatalf("Merge() output missing sections:\n%s", got)
	}
	if rustIdx >= goIdx || goIdx >= pyIdx {
		t.Errorf("sections not in selection order (rust=%d go=%d python=%d):\n%s", rustIdx, goIdx, pyIdx, got)
	}

	if !strings.HasPrefix(got, "# Created by ignit\n") {
		t.Errorf("Merge() output should start with the ignit header, got:\n%s", got)
	}
	if !strings.Contains(got, "# End of https://www.toptal.com/developers/gitignore (created with ignit)\n") {
		t.Errorf("Merge() output missing ignit trailer, got:\n%s", got)
	}
	if strings.Contains(got, "# Edit at ") {
		t.Errorf("Merge() output should not contain the API's '# Edit at' prologue:\n%s", got)
	}
	if strings.Count(got, "# End of ") != 1 {
		t.Errorf("Merge() output should contain exactly one '# End of' trailer:\n%s", got)
	}
}

func TestMergeSingleLanguage(t *testing.T) {
	useCacheDir(t)
	mockAPI(t, map[string]string{
		"go": apiBody("### Go ###\n*.exe\n"),
	})

	got, err := Merge([]string{"go"})
	if err != nil {
		t.Fatalf("Merge() error = %v", err)
	}

	for _, want := range []string{"# Created by ignit", "### Go ###", "*.exe"} {
		if !strings.Contains(got, want) {
			t.Errorf("Merge() output missing %q:\n%s", want, got)
		}
	}
}

func TestMergeDeduplicatesSharedPatterns(t *testing.T) {
	useCacheDir(t)
	mockAPI(t, map[string]string{
		"go":     apiBody("### Go ###\n*.exe\n*.log\ndist/"),
		"node":   apiBody("### Node ###\n*.log\nnode_modules/\ndist/"),
		"python": apiBody("### Python ###\n*.pyc\n__pycache__/"),
	})

	got, err := Merge([]string{"go", "node", "python"})
	if err != nil {
		t.Fatalf("Merge() error = %v", err)
	}

	// Each shared pattern appears exactly once, under the first section that
	// declares it.
	for _, pattern := range []string{"*.log", "dist/", "*.exe", "*.pyc", "node_modules/"} {
		if n := strings.Count(got, pattern+"\n"); n != 1 {
			t.Errorf("pattern %q appears %d times, want 1:\n%s", pattern, n, got)
		}
	}
	// Section headers and comments are never deduplicated.
	if strings.Count(got, "### Go ###") != 1 || strings.Count(got, "### Node ###") != 1 {
		t.Errorf("section headers missing:\n%s", got)
	}
}

func TestMergeDuplicateLanguagesEmitSectionOnce(t *testing.T) {
	useCacheDir(t)
	mockAPI(t, map[string]string{"go": apiBody("### Go ###\n*.exe")})

	got, err := Merge([]string{"go", "go"})
	if err != nil {
		t.Fatalf("Merge() error = %v", err)
	}
	if strings.Count(got, "*.exe") != 1 || strings.Count(got, "### Go ###") != 1 {
		t.Errorf("duplicate selection should collapse to one section:\n%s", got)
	}
}

func TestMergeFetchError(t *testing.T) {
	useCacheDir(t)
	// The server only serves "go"; fetching "rust" must fail the merge.
	mockAPI(t, map[string]string{"go": apiBody("### Go ###\n*.exe")})

	if _, err := Merge([]string{"go", "rust"}); err == nil {
		t.Fatal("Merge() expected error for unknown template, got nil")
	}
}

func TestFetchEmptyBody(t *testing.T) {
	useCacheDir(t)
	mockAPI(t, map[string]string{"go": ""})

	if _, err := Fetch("go"); err == nil {
		t.Fatal("Fetch() expected error for empty body, got nil")
	}
}

func TestFetchNonOKStatus(t *testing.T) {
	useCacheDir(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer server.Close()

	original := API
	API = server.URL
	defer func() { API = original }()

	if _, err := Fetch("go"); err == nil {
		t.Fatal("Fetch() expected error for non-200 status, got nil")
	}
}

func TestCacheSkipsNetworkOnFreshHit(t *testing.T) {
	dir := useCacheDir(t)
	unreachableAPI(t)

	// A fresh cache entry and no reachable API: Fetch must succeed from
	// cache alone.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	putRawCache(t, dir, "go", "### Go ###\n*.exe\n*.log", time.Now())

	got, err := Fetch("go")
	if err != nil {
		t.Fatalf("Fetch() error = %v, want cache hit", err)
	}
	if got != "### Go ###\n*.exe\n*.log" {
		t.Errorf("Fetch() = %q, want cached body", got)
	}
}

func TestCacheRefreshesAfterTTL(t *testing.T) {
	dir := useCacheDir(t)
	CacheTTL = time.Hour
	putRawCache(t, dir, "go", "stale cached body", time.Now().Add(-2*time.Hour))

	// Stale entry: Fetch must go to the network and overwrite the cache.
	mockAPI(t, map[string]string{"go": apiBody("### Go ###\n*.exe")})

	got, err := Fetch("go")
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if !strings.Contains(got, "### Go ###") {
		t.Errorf("Fetch() = %q, want fresh network body", got)
	}

	// The cache stores the raw API body, exactly as returned by the network.
	body, fetchedAt, ok := cacheGet("go")
	if !ok || body != apiBody("### Go ###\n*.exe") {
		t.Errorf("cache not refreshed after fetch (ok=%v body=%q)", ok, body)
	}
	if time.Since(fetchedAt) > time.Minute {
		t.Errorf("cache timestamp not updated: %v", fetchedAt)
	}
}

func TestStaleCacheFallbackOnNetworkError(t *testing.T) {
	dir := useCacheDir(t)
	unreachableAPI(t)
	putRawCache(t, dir, "go", "### Go ###\n*.exe", time.Now().Add(-48*time.Hour))

	// API is unreachable and the entry is stale: fall back to the stale
	// cache instead of erroring.
	got, err := Fetch("go")
	if err != nil {
		t.Fatalf("Fetch() error = %v, want stale fallback", err)
	}
	if got != "### Go ###\n*.exe" {
		t.Errorf("Fetch() = %q, want stale cached body", got)
	}
}

func TestStaleFallbackDisabledWithoutCache(t *testing.T) {
	// No cache available and unreachable API: the fetch error must surface.
	unreachableAPI(t)
	originalDir, originalTTL, originalUser := CacheDir, CacheTTL, userCacheDir
	CacheDir = ""
	CacheTTL = time.Hour
	userCacheDir = func() (string, error) { return "", errors.New("no user cache dir") }
	t.Cleanup(func() {
		CacheDir = originalDir
		CacheTTL = originalTTL
		userCacheDir = originalUser
	})

	if _, err := Fetch("go"); err == nil {
		t.Fatal("Fetch() expected error with unreachable API and no cache, got nil")
	}
}

func TestCacheCorruptEntryIsMiss(t *testing.T) {
	dir := useCacheDir(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.json"), []byte("not json"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, _, ok := cacheGet("go"); ok {
		t.Error("cacheGet() should treat a corrupt entry as a miss")
	}
}

func TestCachePathSanitizesTemplateNames(t *testing.T) {
	dir := "/cache"
	if got := cachePath(dir, "c++"); got != filepath.Join(dir, "c__.json") {
		t.Errorf("cachePath(c++) = %q, want %q", got, filepath.Join(dir, "c__.json"))
	}
	if got := cachePath(dir, "c#"); got != filepath.Join(dir, "c_.json") {
		t.Errorf("cachePath(c#) = %q, want %q", got, filepath.Join(dir, "c_.json"))
	}
	if got := cachePath(dir, "objective-c"); got != filepath.Join(dir, "objective-c.json") {
		t.Errorf("cachePath(objective-c) = %q, want unchanged", got)
	}
}

func TestMergeFetchesConcurrentlyWithinBound(t *testing.T) {
	useCacheDir(t)
	CacheTTL = 0 // never satisfy the freshness check from cache

	const total = 12
	const bound = 4

	bodies := make(map[string]string)
	for i := range total {
		bodies[fmt.Sprintf("lang%02d", i)] = apiBody(fmt.Sprintf("### Lang%02d ###\npattern%02d", i, i))
	}

	var mu sync.Mutex
	inFlight, maxInFlight := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		inFlight++
		if inFlight > maxInFlight {
			maxInFlight = inFlight
		}
		mu.Unlock()

		time.Sleep(20 * time.Millisecond) // widen the window to catch overlaps

		mu.Lock()
		inFlight--
		mu.Unlock()

		body, ok := bodies[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if _, err := fmt.Fprint(w, body); err != nil {
			t.Errorf("failed to write response: %v", err)
		}
	}))
	originalAPI, originalConcurrency := API, MaxConcurrency
	API = server.URL
	MaxConcurrency = bound
	t.Cleanup(func() {
		API = originalAPI
		MaxConcurrency = originalConcurrency
		server.Close()
	})

	languages := make([]string, 0, total)
	for i := range total {
		languages = append(languages, fmt.Sprintf("lang%02d", i))
	}

	got, err := Merge(languages)
	if err != nil {
		t.Fatalf("Merge() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if maxInFlight > bound {
		t.Errorf("concurrency bound exceeded: %d simultaneous requests, max %d", maxInFlight, bound)
	}
	if maxInFlight < 2 {
		t.Errorf("fetches did not overlap (max %d in flight); Merge is not concurrent", maxInFlight)
	}

	// Sections still appear strictly in selection order despite concurrent
	// fetching.
	prev := -1
	for _, lang := range languages {
		idx := strings.Index(got, fmt.Sprintf("### Lang%s ###", strings.TrimPrefix(lang, "lang")))
		if idx < 0 || idx < prev {
			t.Fatalf("sections out of selection order:\n%s", got)
		}
		prev = idx
	}
}

func TestFetchSourceReportsCacheAndNetwork(t *testing.T) {
	dir := useCacheDir(t)
	putRawCache(t, dir, "go", "### Go ###\n*.exe", time.Now())
	mockAPI(t, map[string]string{"rust": apiBody("### Rust ###\ntarget/")})

	// Fresh cache hit: cache origin, network never touched.
	body, source, err := FetchSource("go")
	if err != nil || body != "### Go ###\n*.exe" || source != SourceCache {
		t.Errorf("FetchSource(go) = (%q, %v, %v), want cached body", body, source, err)
	}

	// Not cached: network origin.
	body, source, err = FetchSource("rust")
	if err != nil || !strings.Contains(body, "### Rust ###") || source != SourceNetwork {
		t.Errorf("FetchSource(rust) = (%q, %v, %v), want network body", body, source, err)
	}
}

func TestBypassCacheForcesNetwork(t *testing.T) {
	dir := useCacheDir(t)
	putRawCache(t, dir, "go", "stale cached body", time.Now())
	mockAPI(t, map[string]string{"go": apiBody("### Go ###\n*.exe")})

	original := BypassCache
	BypassCache = true
	t.Cleanup(func() { BypassCache = original })

	body, source, err := FetchSource("go")
	if err != nil || source != SourceNetwork || !strings.Contains(body, "### Go ###") {
		t.Errorf("FetchSource(go) with bypass = (%q, %v, %v), want network body", body, source, err)
	}

	// The bypassed fetch still refreshes the cache entry for next time.
	if cached, _, ok := cacheGet("go"); !ok || cached != apiBody("### Go ###\n*.exe") {
		t.Errorf("cache not refreshed by bypassed fetch (ok=%v)", ok)
	}
}

func TestBypassCacheSkipsStaleFallback(t *testing.T) {
	dir := useCacheDir(t)
	putRawCache(t, dir, "go", "stale cached body", time.Now().Add(-48*time.Hour))
	unreachableAPI(t)

	original := BypassCache
	BypassCache = true
	t.Cleanup(func() { BypassCache = original })

	if _, _, err := FetchSource("go"); err == nil {
		t.Fatal("FetchSource() expected error with bypass and unreachable API, got nil")
	}
}

func TestMergeWithProgressReportsSources(t *testing.T) {
	dir := useCacheDir(t)
	putRawCache(t, dir, "go", "### Go ###\n*.exe", time.Now())
	mockAPI(t, map[string]string{"rust": apiBody("### Rust ###\ntarget/")})

	reported := make(map[string]Source)
	got, err := MergeWithProgress([]string{"go", "rust"}, func(language string, source Source) {
		reported[language] = source
	})
	if err != nil {
		t.Fatalf("MergeWithProgress() error = %v", err)
	}

	if reported["go"] != SourceCache {
		t.Errorf("go source = %v, want %v", reported["go"], SourceCache)
	}
	if reported["rust"] != SourceNetwork {
		t.Errorf("rust source = %v, want %v", reported["rust"], SourceNetwork)
	}
	if !strings.Contains(got, "### Go ###") || !strings.Contains(got, "### Rust ###") {
		t.Errorf("MergeWithProgress() output missing sections:\n%s", got)
	}
}

func TestClearCache(t *testing.T) {
	dir := useCacheDir(t)
	putRawCache(t, dir, "go", "### Go ###\n*.exe", time.Now())

	cleared, err := ClearCache()
	if err != nil {
		t.Fatalf("ClearCache() error = %v", err)
	}
	if cleared != dir {
		t.Errorf("ClearCache() = %q, want %q", cleared, dir)
	}
	if _, _, ok := cacheGet("go"); ok {
		t.Error("cache entry still readable after ClearCache")
	}

	// Clearing an empty/missing cache is a no-op, not an error.
	if _, err := ClearCache(); err != nil {
		t.Errorf("ClearCache() on missing cache error = %v, want nil", err)
	}
}

func TestWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")

	if err := Write(path, "### Go ###\n*.exe\n"); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read written file: %v", err)
	}
	if string(written) != "### Go ###\n*.exe\n" {
		t.Errorf("written content mismatch:\n%s", written)
	}
}

func TestWriteError(t *testing.T) {
	// A directory as target path forces a write error.
	dir := t.TempDir()
	if err := Write(dir, "content"); err == nil {
		t.Fatal("Write() expected error when target is a directory, got nil")
	}
}

// putRawCache stores a cache entry with the given raw body and timestamp.
func putRawCache(t *testing.T, dir string, language string, body string, fetchedAt time.Time) {
	t.Helper()
	entry := cacheEntry{FetchedAt: fetchedAt.Unix(), Body: body}
	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if err := os.WriteFile(cachePath(dir, language), raw, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
