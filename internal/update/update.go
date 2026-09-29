// Package update implements vc's self-update mechanism.
//
// The update model is single-launch: vc probes the GitHub Releases
// version.json, downloads the new binary, checks it against the release's
// SHA256SUMS, and atomically replaces the running binary.  There is no
// banner-and-exit dance (ADR-0002).
package update

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/makscee/void-code/internal/releasesums"
)

// DefaultVersionURL is the canonical version.json location for vc releases.
// Served from void-auth alongside the binary downloads.
const DefaultVersionURL = "https://auth.makscee.ru/vc/version.json"

// DefaultReleaseBaseURL is the base URL for vc release artifacts.
// version.json is at <base>/version.json; binaries at <base>/<artifact-path>.
const DefaultReleaseBaseURL = "https://auth.makscee.ru/vc"

// maxVersionJSONBytes bounds a version.json download; the real one is under 2 KB.
const maxVersionJSONBytes = 1 << 20

// maxBinaryBytes bounds a binary download. vc is about 9 MB.
const maxBinaryBytes = 128 << 20

// DownloadTimeout bounds a whole update (version.json, SHA256SUMS and the
// binary) when Options.Client is nil. A stalled server fails sooner, on
// responseHeaderTimeout.
const DownloadTimeout = 5 * time.Minute

// responseHeaderTimeout is how long the default client waits for a server to
// start answering.
const responseHeaderTimeout = 30 * time.Second

// VersionJSON is the schema for the version.json file published alongside
// each GH Release.
type VersionJSON struct {
	Version   string            `json:"version"`
	Artifacts map[string]string `json:"artifacts"` // platform-key → filename
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
	// Client overrides the HTTP client (for tests). When nil, a client with
	// DownloadTimeout is used.
	Client *http.Client
}

// newClient returns the client CheckAndUpdate uses by default: the whole
// update is bounded by DownloadTimeout, and a server that accepts the
// connection but never answers fails after responseHeaderTimeout.
func newClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.ResponseHeaderTimeout = responseHeaderTimeout
	return &http.Client{Timeout: DownloadTimeout, Transport: transport}
}

// PlatformKey returns the platform identifier used in release artifact names,
// e.g. "darwin/arm64", "linux/amd64", "windows/amd64".
func PlatformKey() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}

// PlatformKeyLegacy returns the legacy (pre-v0.1.2) platform key format used
// by old binaries, e.g. "darwin-arm64".  version.json from v0.1.3+ includes
// both forms as aliases so old clients can self-update.
func PlatformKeyLegacy() string {
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

// ProbeResult is the result of an async version probe.
type ProbeResult struct {
	// HasUpdate is true when a newer version is available.
	HasUpdate bool
	// Latest is the latest version string (e.g. "v0.1.3") when HasUpdate is true.
	Latest string
	// Err is non-nil when the probe failed (network error, parse failure, etc.).
	Err error
}

// ProbeAsync fires a version check in a background goroutine and returns a
// channel that will receive exactly one ProbeResult.  The channel is buffered
// so the goroutine never leaks if the caller discards the result.
//
// timeout controls the HTTP deadline; 0 means no deadline.
// baseURL overrides the default release base URL (empty = use default, for tests).
func ProbeAsync(current, baseURL string, timeout time.Duration) <-chan ProbeResult {
	ch := make(chan ProbeResult, 1)
	go func() {
		ch <- probe(current, baseURL, timeout)
	}()
	return ch
}

// probe performs a synchronous version check.
func probe(current, baseURL string, timeout time.Duration) ProbeResult {
	if baseURL == "" {
		baseURL = DefaultReleaseBaseURL
	}
	client := &http.Client{}
	if timeout > 0 {
		client.Timeout = timeout
	}
	data, err := releasesums.Fetch(client, baseURL+"/version.json", maxVersionJSONBytes)
	if err != nil {
		return ProbeResult{Err: err}
	}
	v, err := ParseVersionJSON(data)
	if err != nil {
		return ProbeResult{Err: err}
	}
	if CompareVersions(current, v.Version) >= 0 {
		return ProbeResult{HasUpdate: false}
	}
	// Normalise latest: ensure it has a "v" prefix.
	latest := v.Version
	if latest != "" && latest[0] != 'v' {
		latest = "v" + latest
	}
	return ProbeResult{HasUpdate: true, Latest: latest}
}

// CheckAndUpdate checks for a new version and, if one is available, downloads
// it, checks its SHA-256 against <base>/SHA256SUMS, and atomically replaces
// the current binary. A binary the list doesn't vouch for is never installed.
//
// Returns (true, nil) when the binary was replaced, (false, nil) when
// already up-to-date, or (false, err) on failure.
func CheckAndUpdate(opts Options) (updated bool, err error) {
	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = DefaultReleaseBaseURL
	}

	client := opts.Client
	if client == nil {
		client = newClient()
	}

	versionURL := baseURL + "/version.json"
	data, err := releasesums.Fetch(client, versionURL, maxVersionJSONBytes)
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
	// Try slash key first ("darwin/arm64"), then legacy hyphen key ("darwin-arm64")
	// for backward compat with releases built before v0.1.3.
	key := PlatformKey()
	filename, ok := v.Artifacts[key]
	if !ok {
		legacyKey := PlatformKeyLegacy()
		filename, ok = v.Artifacts[legacyKey]
		if !ok {
			return false, fmt.Errorf("no release binary for platform %q in version %s", key, v.Version)
		}
	}

	// SHA256SUMS names each file by its base name ("vc-darwin-arm64"), while
	// version.json gives its path under the base ("bin/vc-darwin-arm64").
	binaryURL := baseURL + "/" + filename
	newBin, err := releasesums.Download(client, baseURL+"/SHA256SUMS", binaryURL, path.Base(filename), maxBinaryBytes)
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
