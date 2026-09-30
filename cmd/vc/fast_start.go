package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/config"
)

// Fast chat start (spec docs/superpowers/specs/2026-09-30-vc-fast-start-design.md
// in maks-startup, with «Уточнение Артёма 30.09»).
//
// Two files under ~/.void-code, both keyed by the first 16 hex digits of
// sha256(token) and neither holding the token:
//
//   - providers-<key>.json — the last /v1/vc/providers answer. A start trusts it
//     only while it is younger than providersCacheTTL AND holds the grant the
//     start needs; a missing grant is never an answer (a grant handed out a
//     minute ago must not be refused over the old file). A start served from it
//     refreshes it once in the background after the runtime is spawned.
//   - me-<key>.json — the last successful /v1/vc/me. It marks a token that was
//     let in before: only then does the live access check run beside the start
//     instead of before it. It is also where the launch notice comes from. It is
//     never permission — the check stays live on every launch, and a 401 or 402
//     deletes the file so the next launch waits for the check again.

// providersCacheTTL is how long a cached grants list may answer a start.
const providersCacheTTL = 10 * time.Minute

// providersRefreshDelay is how long after a runtime is spawned the cached
// grants are refreshed: the runtime's own start comes first. A var for tests.
var providersRefreshDelay = 300 * time.Millisecond

// tokenCacheKey names a token's files: the first 16 hex digits of its sha256.
func tokenCacheKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:16]
}

func tokenCachePath(prefix, token string) (string, error) {
	dir, err := config.CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, prefix+"-"+tokenCacheKey(token)+".json"), nil
}

// newLaunchHTTPClient is the one client a launch asks auth through, so /me and
// /providers share its pooled keep-alive connections.
func newLaunchHTTPClient() *http.Client {
	return &http.Client{Timeout: authProbeTimeout}
}

// ─── the providers cache ────────────────────────────────────────────────────

type providersCacheDocument struct {
	Version   int                   `json:"version"`
	FetchedAt time.Time             `json:"fetchedAt"`
	Providers []providersCacheEntry `json:"providers"`
}

type providersCacheEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// readFreshProviders returns the cached grants of token when the cache is
// younger than providersCacheTTL and holds a grant of grantType; anything else
// (no file, unreadable, old, from the future, without that grant) is no answer.
func readFreshProviders(token string, now time.Time, grantType string) ([]auth.ProviderInfo, bool) {
	if strings.TrimSpace(token) == "" {
		return nil, false
	}
	path, err := tokenCachePath("providers", token)
	if err != nil {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var doc providersCacheDocument
	if err := json.Unmarshal(data, &doc); err != nil || doc.Version != 1 || doc.FetchedAt.IsZero() {
		return nil, false
	}
	// A clock set back makes a file look fresh forever; a minute of skew is
	// tolerated, more is not an answer.
	if age := now.Sub(doc.FetchedAt); age >= providersCacheTTL || age < -time.Minute {
		return nil, false
	}
	infos := make([]auth.ProviderInfo, 0, len(doc.Providers))
	for _, p := range doc.Providers {
		infos = append(infos, auth.ProviderInfo{ID: p.ID, Name: p.Name, Type: p.Type})
	}
	if _, found := grantOfType(infos, grantType); !found {
		return nil, false
	}
	return infos, true
}

// grantOfType is the id of the first grant of grantType, in auth's order.
func grantOfType(infos []auth.ProviderInfo, grantType string) (string, bool) {
	for _, info := range infos {
		if strings.EqualFold(strings.TrimSpace(info.Type), grantType) {
			return info.ID, true
		}
	}
	return "", false
}

func writeProvidersCache(token string, infos []auth.ProviderInfo, now time.Time) {
	path, err := tokenCachePath("providers", token)
	if err != nil {
		return
	}
	doc := providersCacheDocument{Version: 1, FetchedAt: now.UTC(), Providers: make([]providersCacheEntry, 0, len(infos))}
	for _, info := range infos {
		doc.Providers = append(doc.Providers, providersCacheEntry{ID: info.ID, Name: info.Name, Type: info.Type})
	}
	if payload, err := json.Marshal(doc); err == nil {
		writeAtomicCache(path, payload)
	}
}

func removeTokenCache(prefix, token string) {
	if path, err := tokenCachePath(prefix, token); err == nil {
		_ = os.Remove(path)
	}
}

// fetchAndCacheProviders asks auth for the live grants and keeps the answer
// for the next start. A 401 drops the cached list with it.
func fetchAndCacheProviders(ctx context.Context, authHost, token string, client *http.Client) ([]auth.ProviderInfo, error) {
	infos, err := fetchProvidersLiveContext(ctx, authHost, token, client)
	if err != nil {
		if errors.Is(err, auth.ErrNotLoggedIn) {
			removeTokenCache("providers", token)
		}
		return nil, err
	}
	writeProvidersCache(token, infos, time.Now())
	return infos, nil
}

// providersRefresher is the background refresh of a start served from the
// cache: one live request, its answer written over the file.
func providersRefresher(authHost, token string, client *http.Client) func(context.Context) {
	return func(ctx context.Context) {
		_, _ = fetchAndCacheProviders(ctx, authHost, token, client)
	}
}

// runAfterSpawn runs refresh once, providersRefreshDelay after the runtime was
// spawned, while it runs. The returned stop cancels a refresh still waiting or
// in flight and returns only once it is over, so nothing outlives the command.
func runAfterSpawn(refresh func(context.Context)) (stop func()) {
	if refresh == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer := time.NewTimer(providersRefreshDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		refresh(ctx)
	}()
	return func() {
		cancel()
		<-done
	}
}

// ─── the saved /me ──────────────────────────────────────────────────────────

type savedMeDocument struct {
	Version int           `json:"version"`
	SavedAt time.Time     `json:"savedAt"`
	Me      auth.MeResult `json:"me"`
}

// readSavedMe is the last successful /v1/vc/me of token, if there is one.
func readSavedMe(token string) (auth.MeResult, bool) {
	if strings.TrimSpace(token) == "" {
		return auth.MeResult{}, false
	}
	path, err := tokenCachePath("me", token)
	if err != nil {
		return auth.MeResult{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return auth.MeResult{}, false
	}
	var doc savedMeDocument
	if err := json.Unmarshal(data, &doc); err != nil || doc.Version != 1 {
		return auth.MeResult{}, false
	}
	return doc.Me, true
}

func writeSavedMe(token string, me auth.MeResult, now time.Time) {
	if strings.TrimSpace(token) == "" {
		return
	}
	path, err := tokenCachePath("me", token)
	if err != nil {
		return
	}
	if payload, err := json.Marshal(savedMeDocument{Version: 1, SavedAt: now.UTC(), Me: me}); err == nil {
		writeAtomicCache(path, payload)
	}
}

// ─── the access check ───────────────────────────────────────────────────────

// admissionGate is authGate's shape (desktopSessionDeps.authGate).
type admissionGate func(string, string, *http.Client) (auth.MeResult, bool, error)

// admit runs the access check and keeps the saved /me in step with its
// answer: a 200 writes it, a 401 or 402 deletes it, an unavailable check
// leaves it (a network blip must not slow the next launch down).
func admit(gate admissionGate, token, host string, client *http.Client) (auth.MeResult, bool, error) {
	me, reached, err := gate(token, host, client)
	switch {
	case err == nil && reached:
		writeSavedMe(token, me, time.Now())
	case errors.Is(err, auth.ErrNotLoggedIn) || errors.Is(err, auth.ErrAccessNotGranted):
		removeTokenCache("me", token)
	}
	return me, reached, err
}

// pendingAdmission is the access check running beside a start. A nil one is
// an admission already passed.
type pendingAdmission struct {
	done chan struct{}
	err  error
}

func startAdmission(gate admissionGate, token, host string, client *http.Client) *pendingAdmission {
	a := &pendingAdmission{done: make(chan struct{})}
	go func() {
		defer close(a.done)
		_, _, a.err = admit(gate, token, host, client)
	}()
	return a
}

// answered is closed once the check has answered; nil (never ready) for an
// admission already passed.
func (a *pendingAdmission) answered() <-chan struct{} {
	if a == nil {
		return nil
	}
	return a.done
}

// wait blocks until the check has answered and returns its refusal, or nil.
func (a *pendingAdmission) wait() error {
	if a == nil {
		return nil
	}
	<-a.done
	return a.err
}

// refusal is the check's refusal when it has already answered with one.
func (a *pendingAdmission) refusal() error {
	if a == nil {
		return nil
	}
	select {
	case <-a.done:
		return a.err
	default:
		return nil
	}
}
