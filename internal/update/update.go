// Package update implements vc's self-update mechanism.
//
// The update model is single-launch: vc probes the GitHub Releases
// version.json, downloads the new binary, and atomically replaces the
// running binary.  There is no banner-and-exit dance (ADR-0002).
package update

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// DefaultVersionURL is the canonical version.json location for vc releases.
const DefaultVersionURL = "https://github.com/makscee/void-code/releases/latest/download/version.json"

// DefaultReleaseBaseURL is the base URL for downloading release binaries.
const DefaultReleaseBaseURL = "https://github.com/makscee/void-code/releases/latest/download"

// VersionJSON is the schema for the version.json file published alongside
// each GH Release.
type VersionJSON struct {
	Version string            `json:"version"`
	Files   map[string]string `json:"files"` // platform-key → filename
}

// Options controls CheckAndUpdate behaviour.
type Options struct {
	// Current is the running binary's version string (e.g. "v0.1.0").
	Current string
	// BaseURL overrides the default release base URL (for tests).
	BaseURL string
	// BinaryPath is the path of the binary to replace.
	// When empty, defaults to os.Executable().
	BinaryPath string
}

// PlatformKey returns the platform identifier used in release artifact names,
// e.g. "darwin-arm64", "linux-amd64", "windows-amd64".
func PlatformKey() string {
	return runtime.GOOS + "-" + runtime.GOARCH
}

// ParseVersionJSON parses a raw version.json payload.
func ParseVersionJSON(data []byte) (VersionJSON, error) {
	var v VersionJSON
	if err := json.Unmarshal(data, &v); err != nil {
		return VersionJSON{}, err
	}
	return v, nil
}

// CompareVersions compares two semver-ish version strings.
// Returns -1 if a < b, 0 if equal, +1 if a > b.
// Strips a leading "v" before parsing.  Non-numeric components are treated as 0.
func CompareVersions(a, b string) int {
	aN := parseVersion(a)
	bN := parseVersion(b)
	for i := range 3 {
		if aN[i] < bN[i] {
			return -1
		}
		if aN[i] > bN[i] {
			return 1
		}
	}
	return 0
}

// CheckAndUpdate checks for a new version and, if one is available, downloads
// it and atomically replaces the current binary.
//
// Returns (true, nil) when the binary was replaced, (false, nil) when
// already up-to-date, or (false, err) on failure.
func CheckAndUpdate(opts Options) (updated bool, err error) {
	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = DefaultReleaseBaseURL
	}

	versionURL := baseURL + "/version.json"
	data, err := fetchURL(versionURL)
	if err != nil {
		return false, fmt.Errorf("fetch version.json: %w", err)
	}

	v, err := ParseVersionJSON(data)
	if err != nil {
		return false, fmt.Errorf("parse version.json: %w", err)
	}

	if CompareVersions(opts.Current, v.Version) >= 0 {
		// Already at latest.
		return false, nil
	}

	// Find the artifact name for our platform.
	key := PlatformKey()
	filename, ok := v.Files[key]
	if !ok {
		return false, fmt.Errorf("no release binary for platform %q in version %s", key, v.Version)
	}

	binaryURL := baseURL + "/" + filename
	newBin, err := fetchURL(binaryURL)
	if err != nil {
		return false, fmt.Errorf("fetch binary %s: %w", filename, err)
	}

	// Resolve the target path.
	dest := opts.BinaryPath
	if dest == "" {
		dest, err = os.Executable()
		if err != nil {
			return false, fmt.Errorf("resolve binary path: %w", err)
		}
	}

	if err := atomicReplace(dest, newBin); err != nil {
		return false, fmt.Errorf("atomic replace: %w", err)
	}

	return true, nil
}

// atomicReplace writes data to a temp file in the same directory as dest,
// then renames it over dest.  This is atomic on most filesystems.
func atomicReplace(dest string, data []byte) error {
	dir := filepath.Dir(dest)
	tmp, err := os.CreateTemp(dir, ".vc-update-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	// Ensure temp file is removed on failure.
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0755); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, dest); err != nil {
		return err
	}
	ok = true
	return nil
}

// fetchURL performs a GET request and returns the body bytes.
func fetchURL(url string) ([]byte, error) {
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}

// parseVersion splits a version string into [major, minor, patch] integers.
func parseVersion(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	var out [3]int
	for i := range 3 {
		if i < len(parts) {
			out[i], _ = strconv.Atoi(parts[i])
		}
	}
	return out
}
