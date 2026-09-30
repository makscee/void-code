package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/makscee/void-code/internal/auth"
)

// Fast start, item 1 (docs/superpowers/specs/2026-09-30-vc-fast-start-design.md):
// /v1/vc/providers is answered from a cache and refreshed in the background.
//
// Contract pinned here (black box, through the file on disk and the fake auth
// server — no clock seam is needed, the age is written into the file):
//
//   - path:   <config.CacheDir()>/providers-<first 16 hex of sha256(token)>.json
//             (~/.void-code/providers-….json); keyed by the token alone;
//   - format: {"version":1,"fetchedAt":<RFC 3339 time, as encoding/json writes
//             time.Time>,"providers":[{"id","name","type"} as auth sends them]};
//   - mode 0600, the token never inside, written atomically (no temp file left
//     next to it);
//   - a start uses the cache only when it is younger than 10 minutes AND holds
//     the grant the start needs (Codex: type openai-codex-oauth). Then no
//     providers request is made before the runtime is spawned, and one refresh
//     is made after the spawn, while the runtime runs, rewriting the file;
//   - otherwise (no file, older than 10 minutes, no needed grant, unreadable,
//     another token's file) the list is fetched live before the spawn, as
//     today, and the file is (re)written;
//   - `vc pi-bootstrap` reads the same cache by the same rules (its needed
//     grant is the same openai-codex-oauth one) and makes no request at all on
//     a fresh cache.

const providersCacheFresh = time.Minute
const providersCacheStale = 11 * time.Minute

type providersCacheDoc struct {
	Version   int                 `json:"version"`
	FetchedAt time.Time           `json:"fetchedAt"`
	Providers []map[string]string `json:"providers"`
}

func tokenHash16(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:16]
}

func providersCacheFile(t *testing.T, token string) string {
	t.Helper()
	return filepath.Join(vcCacheDir(t), "providers-"+tokenHash16(token)+".json")
}

var cachedChatGPTGrant = []map[string]string{
	{"id": "chatgpt-cached", "name": "ChatGPT", "type": "openai-codex-oauth"},
}

var cachedDeepSeekOnly = []map[string]string{
	{"id": "deepseek-cached", "name": "DeepSeek", "type": "deepseek"},
}

func seedProvidersCache(t *testing.T, token string, age time.Duration, providers []map[string]string) time.Time {
	t.Helper()
	fetchedAt := time.Now().Add(-age).UTC().Truncate(time.Second)
	writeProvidersCacheDoc(t, providersCacheFile(t, token), providersCacheDoc{Version: 1, FetchedAt: fetchedAt, Providers: providers})
	return fetchedAt
}

func writeProvidersCacheDoc(t *testing.T, path string, doc providersCacheDoc) {
	t.Helper()
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

// readProvidersCache returns the file as a document, or ok=false when it is
// missing or not the documented JSON.
func readProvidersCache(t *testing.T, token string) (providersCacheDoc, []byte, bool) {
	t.Helper()
	data, err := os.ReadFile(providersCacheFile(t, token))
	if err != nil {
		return providersCacheDoc{}, nil, false
	}
	var doc providersCacheDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return providersCacheDoc{}, data, false
	}
	return doc, data, true
}

func providerIDs(providers []map[string]string) []string {
	ids := make([]string, 0, len(providers))
	for _, p := range providers {
		ids = append(ids, p["id"])
	}
	return ids
}

// assertProvidersCacheFile checks everything the file must be after a live
// answer of want arrived no earlier than since.
func assertProvidersCacheFile(t *testing.T, token string, since time.Time, want []map[string]string) {
	t.Helper()
	path := providersCacheFile(t, token)
	doc, raw, ok := readProvidersCache(t, token)
	if !ok {
		entries, _ := os.ReadDir(filepath.Dir(path))
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("providers cache %s missing or not JSON {version, fetchedAt, providers} (raw %q); cache dir holds %v", path, raw, names)
	}
	if doc.Version != 1 {
		t.Errorf("cache version = %d, want 1", doc.Version)
	}
	if doc.FetchedAt.Before(since.Add(-time.Second)) || doc.FetchedAt.After(time.Now().Add(time.Second)) {
		t.Errorf("cache fetchedAt = %s, want the time of the live answer (after %s)", doc.FetchedAt, since)
	}
	if got, wantIDs := providerIDs(doc.Providers), providerIDs(want); !equalStrings(got, wantIDs) {
		t.Errorf("cached providers = %v, want the live list %v", got, wantIDs)
	}
	for i, p := range doc.Providers {
		if i < len(want) && (p["name"] != want[i]["name"] || p["type"] != want[i]["type"]) {
			t.Errorf("cached provider %d = %v, want it as auth sent it: %v", i, p, want[i])
		}
	}
	if strings.Contains(string(raw), token) {
		t.Errorf("providers cache carries the token: %s", raw)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Errorf("providers cache mode = %04o, want 0600", info.Mode().Perm())
	}
	assertNoCacheTempLeftovers(t, filepath.Base(path))
}

// providersCacheName is a providers cache file of some token.
var providersCacheName = regexp.MustCompile(`^providers-[0-9a-f]{16}\.json$`)

// assertNoCacheTempLeftovers: no temp file of any name is left in the cache
// directory, and every providers file there is a finished one.
func assertNoCacheTempLeftovers(t *testing.T, final string) {
	t.Helper()
	entries, err := os.ReadDir(vcCacheDir(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		lower := strings.ToLower(name)
		if strings.HasPrefix(name, "auth-cache-") {
			continue // the older per-host cache, not this one
		}
		if strings.Contains(lower, "tmp") || strings.Contains(lower, ".partial") ||
			(strings.Contains(lower, "providers") && !providersCacheName.MatchString(name)) {
			t.Errorf("cache directory holds a leftover %q next to %s — the write is not atomic", name, final)
		}
	}
}

// waitFor polls cond until it holds or limit passes, and returns its last value.
func waitFor(limit time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return cond()
}

// ─── CLI Codex ──────────────────────────────────────────────────────────────

// switchLaunch's token (preparePiPathLaunch saves it).
const switchLaunchToken = "admitted-token"

// A fresh cache with the ChatGPT grant starts Codex on that grant without
// waiting for auth; the list is refreshed once, in the background, while Codex
// runs.
func TestCodexStartsFromAFreshProvidersCacheAndRefreshesItAfterSpawn(t *testing.T) {
	var l *switchLaunch
	var refreshed atomic.Bool
	var seeded time.Time
	child := func(ctx context.Context, run *childRun) error {
		// Codex runs until the background refresh has reached auth and landed
		// in the file — or until the test gives up on it.
		refreshed.Store(waitFor(5*time.Second, func() bool {
			l.mu.Lock()
			hits := l.provHits
			l.mu.Unlock()
			doc, _, ok := readProvidersCache(t, switchLaunchToken)
			return hits >= 1 && ok && doc.FetchedAt.After(seeded)
		}))
		return nil
	}
	l = prepareSwitchLaunch(t, meBody(""), child)
	saveRuntimeKey(t, "codex")
	seeded = seedProvidersCache(t, switchLaunchToken, providersCacheFresh, cachedChatGPTGrant)
	started := time.Now()

	if _, err := runSupervised(t); err != nil {
		t.Fatalf("runSpawn: %v", err)
	}
	runs, _, _, provHits := l.snapshot()
	if len(runs) != 1 || runs[0].kind != "codex" {
		t.Fatalf("children = %v, want one Codex", kinds(runs))
	}
	if runs[0].provHitsAtSpawn != 0 {
		t.Errorf("auth was asked for providers %d time(s) before Codex started; a fresh cache with the grant must not wait for the network", runs[0].provHitsAtSpawn)
	}
	wantEnvOnce(t, runs[0].env, "VC_CODEX_PROVIDER", "chatgpt-cached")
	if !refreshed.Load() {
		t.Fatalf("the cache was not refreshed in the background while Codex ran (providers hits = %d)", provHits)
	}
	if provHits != 1 {
		t.Errorf("providers asked %d times, want exactly one background refresh", provHits)
	}
	assertProvidersCacheFile(t, switchLaunchToken, started, codexGrants)
}

// Anything but a fresh cache holding the grant is a live question before the
// spawn, as today, and the answer is written for the next start.
func TestCodexAsksAuthBeforeSpawnWhenTheCacheCannotAnswer(t *testing.T) {
	for _, tc := range []struct {
		name string
		seed func(t *testing.T)
	}{
		{"no cache file", func(t *testing.T) {}},
		{"cache older than 10 minutes", func(t *testing.T) {
			seedProvidersCache(t, switchLaunchToken, providersCacheStale, cachedChatGPTGrant)
		}},
		// A negative answer from the cache is never used: a grant handed out a
		// minute ago must not be refused over the old file.
		{"fresh cache without the ChatGPT grant", func(t *testing.T) {
			seedProvidersCache(t, switchLaunchToken, providersCacheFresh, cachedDeepSeekOnly)
		}},
		{"fresh cache of another token", func(t *testing.T) {
			seedProvidersCache(t, "someone-elses-token", providersCacheFresh, cachedChatGPTGrant)
		}},
		{"unreadable cache", func(t *testing.T) {
			path := providersCacheFile(t, switchLaunchToken)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := prepareSwitchLaunch(t, meBody(""), exitWith(0))
			saveRuntimeKey(t, "codex")
			tc.seed(t)
			started := time.Now()

			if _, err := runSupervised(t); err != nil {
				t.Fatalf("runSpawn: %v", err)
			}
			runs, _, _, _ := l.snapshot()
			if len(runs) != 1 || runs[0].kind != "codex" {
				t.Fatalf("children = %v, want one Codex", kinds(runs))
			}
			if runs[0].provHitsAtSpawn != 1 {
				t.Errorf("providers asked %d time(s) before Codex started, want exactly 1 live request", runs[0].provHitsAtSpawn)
			}
			wantEnvOnce(t, runs[0].env, "VC_CODEX_PROVIDER", "chatgpt-new")
			assertProvidersCacheFile(t, switchLaunchToken, started, codexGrants)
		})
	}
}

// The desktop's Codex chat reads the same cache: nothing is asked of auth
// before the chat's Codex is started.
func TestDesktopCodexStartsFromAFreshProvidersCache(t *testing.T) {
	p, _, _ := prepareDesktopCodex(t, codexGrants)
	seedProvidersCache(t, desktopCodexToken, providersCacheFresh, cachedChatGPTGrant)
	deps := p.deps()
	askedBeforeRun := false
	deps.run = func(_ context.Context, plan desktopSessionPlan, _ io.Reader, _, _ io.Writer) error {
		askedBeforeRun = p.called("providers")
		p.note("run")
		p.ran = true
		p.plan = plan
		return nil
	}

	if _, err := execDesktopSessionArgs(t, deps, "--runtime", "codex", "--"); err != nil {
		t.Fatalf("desktop-session --runtime codex: %v; steps=%v", err, p.steps())
	}
	if !p.ran {
		t.Fatal("Codex was never launched")
	}
	if askedBeforeRun {
		t.Errorf("auth was asked for providers before the desktop Codex started; steps=%v", p.steps())
	}
	wantEnvOnce(t, p.plan.env, "VC_CODEX_PROVIDER", "chatgpt-cached")
}

// ─── vc pi-bootstrap ────────────────────────────────────────────────────────

type countingProviders struct {
	hits   atomic.Int32
	grants []map[string]string
}

func servePiBootstrapProviders(t *testing.T, token string, grants []map[string]string) *countingProviders {
	t.Helper()
	c := &countingProviders{grants: grants}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.hits.Add(1)
		if r.URL.Path != "/v1/vc/providers" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"providers": c.grants})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("VC_AUTH_HOST", srv.URL)
	t.Setenv("VC_ACCESS_CHECK_HOST", srv.URL)
	t.Setenv("VC_RELAY_HOST", "https://relay.test:9443")
	if err := auth.Save(token); err != nil {
		t.Fatal(err)
	}
	return c
}

func bootstrapIDs(b piBootstrap) []string {
	var ids []string
	for _, p := range b.Providers {
		ids = append(ids, p.RelayProviderID)
	}
	return ids
}

const piBootstrapCacheToken = "pi-bootstrap-cache-token"

var liveBootstrapGrants = []map[string]string{
	{"id": "chatgpt-live", "name": "ChatGPT", "type": "openai-codex-oauth"},
}

func TestPiBootstrapUsesAFreshProvidersCacheWithoutTheNetwork(t *testing.T) {
	withTempHome(t)
	live := servePiBootstrapProviders(t, piBootstrapCacheToken, liveBootstrapGrants)
	seedProvidersCache(t, piBootstrapCacheToken, providersCacheFresh, cachedChatGPTGrant)

	got, err := currentPiBootstrap()
	if err != nil {
		t.Fatalf("currentPiBootstrap: %v", err)
	}
	if n := live.hits.Load(); n != 0 {
		t.Errorf("pi-bootstrap asked auth %d time(s) although a fresh cache holds the grant", n)
	}
	if ids := bootstrapIDs(got); !equalStrings(ids, []string{"chatgpt-cached"}) {
		t.Errorf("bootstrap providers = %v, want the cached grant [chatgpt-cached]", ids)
	}
	if got.AuthToken != piBootstrapCacheToken || got.Version != 1 || got.RelayURL != "https://relay.test:9443" {
		t.Errorf("bootstrap metadata = %+v", got)
	}
}

func TestPiBootstrapAsksAuthWhenTheCacheCannotAnswer(t *testing.T) {
	for _, tc := range []struct {
		name string
		seed func(t *testing.T)
	}{
		{"no cache file", func(t *testing.T) {}},
		{"cache older than 10 minutes", func(t *testing.T) {
			seedProvidersCache(t, piBootstrapCacheToken, providersCacheStale, cachedChatGPTGrant)
		}},
		{"fresh cache without the ChatGPT grant", func(t *testing.T) {
			seedProvidersCache(t, piBootstrapCacheToken, providersCacheFresh, cachedDeepSeekOnly)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withTempHome(t)
			live := servePiBootstrapProviders(t, piBootstrapCacheToken, liveBootstrapGrants)
			tc.seed(t)
			started := time.Now()

			got, err := currentPiBootstrap()
			if err != nil {
				t.Fatalf("currentPiBootstrap: %v", err)
			}
			if n := live.hits.Load(); n != 1 {
				t.Errorf("pi-bootstrap asked auth %d time(s), want exactly 1 live request", n)
			}
			if ids := bootstrapIDs(got); !equalStrings(ids, []string{"chatgpt-live"}) {
				t.Errorf("bootstrap providers = %v, want the live grant [chatgpt-live]", ids)
			}
			assertProvidersCacheFile(t, piBootstrapCacheToken, started, liveBootstrapGrants)
		})
	}
}
