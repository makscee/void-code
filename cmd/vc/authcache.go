package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/config"
)

const (
	// authProbeTimeout is one live call to the auth service. A healthy relay
	// answers /v1/vc/me in well under a second, so this only shows on a slow
	// network, where 2s turned a momentary stall into a refused launch.
	authProbeTimeout = 5 * time.Second
)

// authAdmissionBound caps the whole admission check, retry included: two
// authProbeTimeout attempts at most, never longer than this in total. A var so
// tests can shrink it instead of waiting out the real bound.
var authAdmissionBound = 2 * authProbeTimeout

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

func removeAuthCacheFile(kind, authHost, token string) {
	path, err := authCachePath(kind, authHost, token)
	if err == nil {
		_ = os.Remove(path)
	}
}

// clearAuthCache removes the on-disk cache files an older vc wrote for this
// host and token. Nothing reads them any more.
func clearAuthCache(kind, authHost, token string) {
	removeAuthCacheFile(kind, authHost, token)
	removeAuthCacheFile(kind+"-transient", authHost, token)
}

// fetchProvidersLive always asks the auth service for the current grants.
// Provider grants can change immediately after login, so even a fresh empty
// cache entry is not authoritative.
func fetchProvidersLive(authHost, token string, httpClient *http.Client) ([]auth.ProviderInfo, error) {
	providers, err := auth.FetchProviders(authHost, token, httpClient)
	if err != nil {
		if errors.Is(err, auth.ErrNotLoggedIn) {
			clearAuthCache("providers", authHost, token)
		}
		return nil, err
	}
	clearAuthCache("providers", authHost, token)
	return providers, nil
}
