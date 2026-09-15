// Package gitignore downloads gitignore.io templates and merges them
// client-side, so section order and headers are controlled locally instead
// of by the API. Templates are fetched concurrently with a bounded pool,
// ignore patterns repeated across templates are emitted only once, and
// fetched templates are cached on disk so repeated runs with the same
// selection skip the network.
package gitignore

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// API is the gitignore.io API base URL. A var so tests can point it at an
// httptest server.
var API = "https://www.gitignore.io/api"

// Fetch tunables, vars so tests can adjust them. When CacheDir is empty the
// per-user cache directory is used; if that is unavailable, caching is
// disabled and every fetch goes to the network.
var (
	// CacheTTL is how long a cached template stays fresh.
	CacheTTL = 24 * time.Hour
	// CacheDir overrides the template cache directory (tests).
	CacheDir = ""
	// MaxConcurrency bounds how many templates Merge fetches at once.
	MaxConcurrency = 8
	// BypassCache makes FetchSource skip cache reads entirely: every fetch
	// goes to the network and refreshes the cache entry on success.
	BypassCache = false
)

const (
	// header opens the merged .gitignore.
	header = "# Created by ignit\n" +
		"# Merged from https://www.toptal.com/developers/gitignore\n"

	// trailer closes the merged .gitignore.
	trailer = "\n# End of https://www.toptal.com/developers/gitignore (created with ignit)\n"
)

// cacheEntry is the on-disk record for one fetched template.
type cacheEntry struct {
	FetchedAt int64  `json:"fetched_at"`
	Body      string `json:"body"`
}

// Source describes where a fetched template body came from.
type Source string

const (
	// SourceNetwork means the body was downloaded from the API.
	SourceNetwork Source = "network"
	// SourceCache means the body came from the disk cache — either a fresh
	// hit or a stale fallback when the network failed.
	SourceCache Source = "cache"
)

// Fetch downloads the gitignore.io template for a single language, going
// through the disk cache: a fresh cache hit skips the network entirely, and
// a failed fetch falls back to a stale cache entry when one exists.
func Fetch(language string) (string, error) {
	body, _, err := FetchSource(language)
	return body, err
}

// FetchSource is Fetch with the origin of the body reported.
func FetchSource(language string) (string, Source, error) {
	if !BypassCache {
		if body, fetchedAt, ok := cacheGet(language); ok && time.Since(fetchedAt) < CacheTTL {
			return body, SourceCache, nil
		}
	}

	body, err := fetchRemote(language)
	if err != nil {
		// Offline or API trouble: a stale cached template beats a hard error.
		if !BypassCache {
			if stale, _, ok := cacheGet(language); ok {
				return stale, SourceCache, nil
			}
		}
		return "", SourceNetwork, err
	}

	cachePut(language, body)
	return body, SourceNetwork, nil
}

// fetchRemote downloads the raw template for a single language, bypassing
// the cache.
func fetchRemote(language string) (string, error) {
	resp, err := http.Get(API + "/" + language)
	if err != nil {
		return "", fmt.Errorf("failed to fetch gitignore for %s: %w", language, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch gitignore for %s: unexpected status %s", language, resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read gitignore response: %w", err)
	}

	if strings.TrimSpace(string(body)) == "" {
		return "", fmt.Errorf("empty gitignore template for %s", language)
	}
	return string(body), nil
}

// Merge assembles a single .gitignore document from the given languages:
// templates are fetched concurrently (bounded by MaxConcurrency) and their
// sections are written in selection order, with patterns repeated across
// templates emitted only once. gitignore.io's per-request header and trailer
// are stripped locally, so the output carries a single ignit header.
func Merge(languages []string) (string, error) {
	return MergeWithProgress(languages, nil)
}

// MergeWithProgress is Merge with an optional onFetch callback, invoked once
// per template as its fetch completes with the source the body came from.
// Callbacks are serialized; onFetch may be nil.
func MergeWithProgress(languages []string, onFetch func(language string, source Source)) (string, error) {
	// A repeated selection is fetched and emitted only once; the first
	// occurrence wins.
	unique := make([]string, 0, len(languages))
	selected := make(map[string]bool, len(languages))
	for _, language := range languages {
		if !selected[language] {
			selected[language] = true
			unique = append(unique, language)
		}
	}

	bodies, err := fetchAll(unique, onFetch)
	if err != nil {
		return "", err
	}

	seen := make(map[string]bool)
	sections := make([]string, 0, len(bodies))
	for _, body := range bodies {
		section := dedupeLines(stripWrapper(body), seen)
		if section != "" {
			sections = append(sections, section)
		}
	}

	var b strings.Builder
	b.WriteString(header)
	for _, section := range sections {
		b.WriteString("\n")
		b.WriteString(section)
		b.WriteString("\n")
	}
	b.WriteString(trailer)
	return b.String(), nil
}

// fetchAll fetches every language concurrently, bounded by MaxConcurrency,
// and returns the bodies in selection order. The first error in selection
// order aborts the merge. As each fetch completes, onFetch (if non-nil) is
// invoked with the source of the body; callbacks are serialized.
func fetchAll(languages []string, onFetch func(language string, source Source)) ([]string, error) {
	bodies := make([]string, len(languages))
	errs := make([]error, len(languages))

	limit := MaxConcurrency
	if limit <= 0 || limit > len(languages) {
		limit = len(languages)
	}
	if limit < 1 {
		limit = 1
	}

	sem := make(chan struct{}, limit)
	var mu sync.Mutex // serializes onFetch callbacks
	var wg sync.WaitGroup
	for i, language := range languages {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			body, source, err := FetchSource(language)
			bodies[i], errs[i] = body, err
			if err == nil && onFetch != nil {
				mu.Lock()
				onFetch(language, source)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return bodies, nil
}

// dedupeLines drops pattern lines already emitted by an earlier section,
// keeping the first occurrence in selection order. Comments (including
// section headers) and blank lines are always kept.
func dedupeLines(body string, seen map[string]bool) string {
	lines := strings.Split(body, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		key := strings.TrimSpace(line)
		if key != "" && !strings.HasPrefix(key, "#") {
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// Write writes the merged content to path.
func Write(path string, content string) error {
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("failed to write .gitignore: %w", err)
	}
	return nil
}

// ClearCache deletes the template cache directory and returns its path.
// Clearing a cache that does not exist is a no-op, not an error.
func ClearCache() (string, error) {
	dir := cacheDir()
	if dir == "" {
		return "", fmt.Errorf("template cache is unavailable")
	}
	if err := os.RemoveAll(dir); err != nil {
		return dir, fmt.Errorf("failed to clear template cache: %w", err)
	}
	return dir, nil
}

// userCacheDir is the OS cache-directory lookup, a var so tests can stub it.
var userCacheDir = os.UserCacheDir

// cacheDir resolves the template cache directory, or "" when caching is
// unavailable.
func cacheDir() string {
	if CacheDir != "" {
		return CacheDir
	}
	userDir, err := userCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(userDir, "ignit", "templates")
}

// cachePath is the cache file for a language; names are sanitized so odd
// template names (c++, c#, ...) stay safe as file names.
func cachePath(dir string, language string) string {
	name := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			return r
		default:
			return '_'
		}
	}, language)
	return filepath.Join(dir, name+".json")
}

// cacheGet returns a cached template, if any. Corrupt or empty entries are
// treated as misses.
func cacheGet(language string) (string, time.Time, bool) {
	dir := cacheDir()
	if dir == "" {
		return "", time.Time{}, false
	}
	raw, err := os.ReadFile(cachePath(dir, language))
	if err != nil {
		return "", time.Time{}, false
	}
	var entry cacheEntry
	if err := json.Unmarshal(raw, &entry); err != nil || entry.Body == "" {
		return "", time.Time{}, false
	}
	return entry.Body, time.Unix(entry.FetchedAt, 0), true
}

// cachePut stores a template, best effort: cache failures never fail a fetch.
func cachePut(language string, body string) {
	dir := cacheDir()
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	entry := cacheEntry{FetchedAt: time.Now().Unix(), Body: body}
	raw, err := json.Marshal(entry)
	if err != nil {
		return
	}
	_ = os.WriteFile(cachePath(dir, language), raw, 0o644)
}

// stripWrapper removes gitignore.io's response wrapper — the "# Created by" /
// "# Edit at" prologue and the "# End of" trailer — leaving the template
// sections. Bodies without the markers are returned unchanged.
func stripWrapper(body string) string {
	lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")

	start := 0
	for i, line := range lines {
		if strings.HasPrefix(line, "# Edit at ") {
			start = i + 1
			break
		}
	}
	lines = lines[start:]

	for i, line := range lines {
		if strings.HasPrefix(line, "# End of ") {
			lines = lines[:i]
			break
		}
	}

	// Drop the blank separator line(s) between the prologue and the first
	// template section, and any trailing blank lines, so merged sections sit
	// cleanly between the ignit header and trailer.
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}
