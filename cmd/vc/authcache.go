package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/config"
)

const (
	authCacheTTL          = 5 * time.Minute
	authCacheTransientTTL = 30 * time.Second
	authProbeTimeout      = 2 * time.Second
)

var errAuthTemporarilyUnavailable = errors.New("identity temporarily unavailable")

type authCacheEnvelope[T any] struct {
	ExpiresAt time.Time `json:"expiresAt"`
	Value     T         `json:"value"`
}

type lastKnownIdentity struct {
	UserID string `json:"userId,omitempty"`
	Email  string `json:"email,omitempty"`
}

type meCacheRecord struct {
	FreshExpiresAt time.Time         `json:"freshExpiresAt"`
	Fresh          auth.MeResult     `json:"fresh"`
	LastKnown      lastKnownIdentity `json:"lastKnown"`
}

type cachedMeState struct {
	Me    auth.MeResult
	Stale bool
}

func authCacheKey(authHost, token string) string {
	sum := sha256.Sum256([]byte(authHost + "\x00" + token))
	return hex.EncodeToString(sum[:])
}

func authCachePath(kind, authHost, token string) (string, error) {
	dir, err := config.CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "auth-cache-"+kind+"-"+authCacheKey(authHost, token)+".json"), nil
}

func readAuthCache[T any](kind, authHost, token string, now time.Time) (T, bool) {
	var zero T
	path, err := authCachePath(kind, authHost, token)
	if err != nil {
		return zero, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return zero, false
	}
	var env authCacheEnvelope[T]
	if err := json.Unmarshal(data, &env); err != nil || !now.Before(env.ExpiresAt) {
		return zero, false
	}
	return env.Value, true
}

func writeAtomicCache(path string, payload []byte) bool {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false
	}
	_ = os.Chmod(dir, 0o700)
	tmp, err := os.CreateTemp(dir, ".auth-cache-*")
	if err != nil {
		return false
	}
	tmpPath := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return false
	}
	if _, err := tmp.Write(payload); err != nil {
		return false
	}
	if err := tmp.Sync(); err != nil {
		return false
	}
	if err := tmp.Close(); err != nil {
		return false
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return false
	}
	ok = true
	return true
}

func writeAuthCache[T any](kind, authHost, token string, value T, now time.Time) {
	path, err := authCachePath(kind, authHost, token)
	if err != nil {
		return
	}
	payload, err := json.Marshal(authCacheEnvelope[T]{ExpiresAt: now.Add(authCacheTTL), Value: value})
	if err == nil {
		writeAtomicCache(path, payload)
	}
}

func readMeCache(authHost, token string, now time.Time) (cachedMeState, bool) {
	path, err := authCachePath("me", authHost, token)
	if err != nil {
		return cachedMeState{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cachedMeState{}, false
	}
	var record meCacheRecord
	if err := json.Unmarshal(data, &record); err == nil && !record.FreshExpiresAt.IsZero() {
		if now.Before(record.FreshExpiresAt) {
			return cachedMeState{Me: record.Fresh}, true
		}
		if record.LastKnown.Email != "" || record.LastKnown.UserID != "" {
			return cachedMeState{Me: auth.MeResult{Email: record.LastKnown.Email, UserID: record.LastKnown.UserID}, Stale: true}, true
		}
		return cachedMeState{}, false
	}

	// Preserve identity from the previous cache schema after its fresh TTL expires.
	var legacy authCacheEnvelope[auth.MeResult]
	if err := json.Unmarshal(data, &legacy); err != nil {
		return cachedMeState{}, false
	}
	if now.Before(legacy.ExpiresAt) {
		return cachedMeState{Me: legacy.Value}, true
	}
	if legacy.Value.Email != "" || legacy.Value.UserID != "" {
		return cachedMeState{Me: auth.MeResult{Email: legacy.Value.Email, UserID: legacy.Value.UserID}, Stale: true}, true
	}
	return cachedMeState{}, false
}

func writeMeCache(authHost, token string, me auth.MeResult, now time.Time) {
	path, err := authCachePath("me", authHost, token)
	if err != nil {
		return
	}
	record := meCacheRecord{
		FreshExpiresAt: now.Add(authCacheTTL),
		Fresh:          me,
		LastKnown:      lastKnownIdentity{UserID: me.UserID, Email: me.Email},
	}
	payload, err := json.Marshal(record)
	if err == nil {
		writeAtomicCache(path, payload)
	}
}

func removeAuthCacheFile(kind, authHost, token string) {
	path, err := authCachePath(kind, authHost, token)
	if err == nil {
		_ = os.Remove(path)
	}
}

func clearAuthCache(kind, authHost, token string) {
	removeAuthCacheFile(kind, authHost, token)
	removeAuthCacheFile(kind+"-transient", authHost, token)
}

func readAuthTransient(kind, authHost, token string) error {
	message, ok := readAuthCache[string](kind+"-transient", authHost, token, time.Now())
	if !ok || message == "" {
		return nil
	}
	return errAuthTemporarilyUnavailable
}

func writeAuthTransient(kind, authHost, token string, _ error) {
	path, err := authCachePath(kind+"-transient", authHost, token)
	if err != nil {
		return
	}
	payload, err := json.Marshal(authCacheEnvelope[string]{ExpiresAt: time.Now().Add(authCacheTransientTTL), Value: "temporarily unavailable"})
	if err == nil {
		writeAtomicCache(path, payload)
	}
}

func isIdentityToken(token string) bool {
	separator := strings.IndexByte(token, '.')
	return separator > 0 && separator < len(token)-1 && strings.Count(token, ".") == 1
}

func cachedFetchMeState(authHost, token string, httpClient *http.Client) (cachedMeState, error) {
	now := time.Now()
	cached, hasCached := readMeCache(authHost, token, now)
	if hasCached && !cached.Stale {
		return cached, nil
	}
	if err := readAuthTransient("me", authHost, token); err != nil {
		return cached, err
	}
	me, err := auth.FetchMe(authHost, token, httpClient)
	if err != nil {
		if errors.Is(err, auth.ErrNotLoggedIn) {
			if !isIdentityToken(token) {
				clearAuthCache("me", authHost, token)
			}
		} else {
			writeAuthTransient("me", authHost, token, err)
		}
		return cached, err
	}
	writeMeCache(authHost, token, me, now)
	removeAuthCacheFile("me-transient", authHost, token)
	return cachedMeState{Me: me}, nil
}

func cachedFetchMe(authHost, token string, httpClient *http.Client) (auth.MeResult, error) {
	state, err := cachedFetchMeState(authHost, token, httpClient)
	if err != nil {
		return auth.MeResult{}, err
	}
	return state.Me, nil
}

func cachedFetchProviders(authHost, token string, httpClient *http.Client) ([]auth.ProviderInfo, error) {
	if cached, ok := readAuthCache[[]auth.ProviderInfo]("providers", authHost, token, time.Now()); ok {
		return cached, nil
	}
	if err := readAuthTransient("providers", authHost, token); err != nil {
		return nil, err
	}
	providers, err := auth.FetchProviders(authHost, token, httpClient)
	if err != nil {
		if errors.Is(err, auth.ErrNotLoggedIn) {
			clearAuthCache("providers", authHost, token)
		} else {
			writeAuthTransient("providers", authHost, token, err)
		}
		return nil, err
	}
	writeAuthCache("providers", authHost, token, providers, time.Now())
	return providers, nil
}

func authCacheDebugPath(kind, authHost, token string) (string, error) {
	path, err := authCachePath(kind, authHost, token)
	if err != nil {
		return "", fmt.Errorf("auth cache path: %w", err)
	}
	return path, nil
}
