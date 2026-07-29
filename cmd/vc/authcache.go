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
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/config"
)

const (
	authCacheTTL          = 5 * time.Minute
	authCacheTransientTTL = 30 * time.Second
	authProbeTimeout      = 2 * time.Second
)

type authCacheEnvelope[T any] struct {
	ExpiresAt time.Time `json:"expiresAt"`
	Value     T         `json:"value"`
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
	if err := json.Unmarshal(data, &env); err != nil {
		return zero, false
	}
	if !now.Before(env.ExpiresAt) {
		return zero, false
	}
	return env.Value, true
}

func writeAuthCache[T any](kind, authHost, token string, value T, now time.Time) {
	path, err := authCachePath(kind, authHost, token)
	if err != nil {
		return
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	_ = os.Chmod(dir, 0o700)
	payload, err := json.Marshal(authCacheEnvelope[T]{ExpiresAt: now.Add(authCacheTTL), Value: value})
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, ".auth-cache-*")
	if err != nil {
		return
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
		return
	}
	if _, err := tmp.Write(payload); err != nil {
		return
	}
	if err := tmp.Sync(); err != nil {
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return
	}
	ok = true
}

func clearAuthCachesForToken(authHost, token string) {
	for _, kind := range []string{"me", "providers", "auth-me"} {
		clearAuthCache(kind, authHost, token)
	}
}

func clearAuthCache(kind, authHost, token string) {
	path, err := authCachePath(kind, authHost, token)
	if err == nil {
		_ = os.Remove(path)
	}
	path, err = authCachePath(kind+"-transient", authHost, token)
	if err == nil {
		_ = os.Remove(path)
	}
}

func readAuthTransient(kind, authHost, token string) error {
	message, ok := readAuthCache[string](kind+"-transient", authHost, token, time.Now())
	if !ok || message == "" {
		return nil
	}
	return fmt.Errorf("recent auth-host failure: %s", message)
}

func writeAuthTransient(kind, authHost, token string, err error) {
	path, pathErr := authCachePath(kind+"-transient", authHost, token)
	if pathErr != nil {
		return
	}
	dir := filepath.Dir(path)
	if mkdirErr := os.MkdirAll(dir, 0o700); mkdirErr != nil {
		return
	}
	payload, marshalErr := json.Marshal(authCacheEnvelope[string]{ExpiresAt: time.Now().Add(authCacheTransientTTL), Value: err.Error()})
	if marshalErr != nil {
		return
	}
	_ = os.WriteFile(path, payload, 0o600)
}

func cachedFetchMe(authHost, token string, httpClient *http.Client) (auth.MeResult, error) {
	if cached, ok := readAuthCache[auth.MeResult]("me", authHost, token, time.Now()); ok {
		return cached, nil
	}
	if err := readAuthTransient("me", authHost, token); err != nil {
		return auth.MeResult{}, err
	}
	me, err := auth.FetchMe(authHost, token, httpClient)
	if err != nil {
		if errors.Is(err, auth.ErrNotLoggedIn) {
			clearAuthCache("me", authHost, token)
		} else {
			writeAuthTransient("me", authHost, token, err)
		}
		return auth.MeResult{}, err
	}
	writeAuthCache("me", authHost, token, me, time.Now())
	return me, nil
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
