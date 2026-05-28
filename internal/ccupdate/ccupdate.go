// Package ccupdate checks and installs the latest @anthropic-ai/claude-code
// npm package before vc spawns claude.
//
// Design (VCD-33):
//   - Compare installed vs latest version via npm CLI
//   - If stale: npm install -g @anthropic-ai/claude-code@latest
//   - 1h TTL cache (sentinel file ~/.void-code/last-cc-update-check)
//   - All npm calls use a 3s timeout
//   - Network unreachable → silent skip
//   - HTTP/npm error → print one-line warning, proceed
//   - Successful update → print "claude-code: vX → vY"
package ccupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const (
	pkg           = "@anthropic-ai/claude-code"
	defaultTTL    = time.Hour
	envTTL        = "VC_UPDATE_CHECK_TTL_S"
	npmCallTimeout = 3 * time.Second
	installTimeout = 5 * time.Minute // npm install can take a while
)

// CachePath is the sentinel file for the claude-code update check TTL.
// Set by the caller (e.g. via config.CacheDir) before calling CheckAndUpdate.
var CachePath = ""

// CheckAndUpdate checks the installed claude-code version against npm registry
// and installs the latest if stale. Returns a human-readable result line
// (e.g. "claude-code: v1.1.0 → v1.2.3") on update, "" on no-op, or
// "auto-update failed: <reason>" on a real error (not timeout/offline).
//
// Callers should call this before spawning claude and print the non-empty
// return value for the user.
func CheckAndUpdate() string {
	if cacheFresh() {
		return ""
	}
	touchCache()

	installed, err := installedVersion()
	if err != nil {
		// npm not found or list failed — can't update, silent skip.
		return ""
	}

	latest, err := latestVersion()
	if err != nil {
		// Network unavailable or registry unreachable — silent skip.
		// Only surface if it looks like a definitive server error.
		if isHardError(err) {
			return fmt.Sprintf("auto-update failed: %v", err)
		}
		return ""
	}

	if compareVersions(installed, latest) >= 0 {
		return "" // already at latest
	}

	// Install.
	if err := install(); err != nil {
		return fmt.Sprintf("auto-update failed: %v", err)
	}

	from := installed
	if from == "" {
		from = "unknown"
	}
	return fmt.Sprintf("claude-code: v%s → v%s", strings.TrimPrefix(from, "v"), strings.TrimPrefix(latest, "v"))
}

// npmBin returns the npm binary name: "npm.cmd" on Windows, "npm" elsewhere.
// This matches the VCD-25 pattern for Windows execution-policy safety.
func npmBin() string {
	if runtime.GOOS == "windows" {
		return "npm.cmd"
	}
	return "npm"
}

// installedVersion returns the locally installed version of @anthropic-ai/claude-code,
// or "" if not installed.
func installedVersion() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), npmCallTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, npmBin(), "list", "-g", "--depth=0", "--json")
	var out bytes.Buffer
	cmd.Stdout = &out
	// Ignore exit code — npm list returns non-zero when packages are missing.
	_ = cmd.Run()

	if ctx.Err() != nil {
		// Timeout — treat as not found.
		return "", nil
	}

	var result struct {
		Dependencies map[string]struct {
			Version string `json:"version"`
		} `json:"dependencies"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		return "", nil // malformed output — treat as unknown
	}
	if dep, ok := result.Dependencies[pkg]; ok {
		return dep.Version, nil
	}
	return "", nil // not installed
}

// latestVersion queries the npm registry for the latest published version.
func latestVersion() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), npmCallTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, npmBin(), "view", pkg, "version")
	var out bytes.Buffer
	var errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			// Timeout — treat as offline.
			return "", fmt.Errorf("timeout")
		}
		// Non-zero exit — might be a real error.
		return "", fmt.Errorf("npm view: %s", strings.TrimSpace(errBuf.String()))
	}

	ver := strings.TrimSpace(out.String())
	if ver == "" {
		return "", fmt.Errorf("npm view returned empty version")
	}
	return ver, nil
}

// install runs npm install -g @anthropic-ai/claude-code@latest.
func install() error {
	ctx, cancel := context.WithTimeout(context.Background(), installTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, npmBin(), "install", "-g", pkg+"@latest")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// isHardError reports whether err is a definitive server/client error
// (4xx/5xx) rather than a transient network issue (timeout, DNS failure).
func isHardError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	// npm errors for 404/403/500 contain these strings:
	return strings.Contains(s, "404") ||
		strings.Contains(s, "403") ||
		strings.Contains(s, "500") ||
		strings.Contains(s, "E404") ||
		strings.Contains(s, "ENOENT") // package not found
}

// cacheFresh returns true when the TTL sentinel is fresh.
func cacheFresh() bool {
	if CachePath == "" {
		return false
	}
	info, err := os.Stat(CachePath)
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) < ttl()
}

// touchCache creates/updates the TTL sentinel file.
func touchCache() {
	if CachePath == "" {
		return
	}
	f, err := os.OpenFile(CachePath, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	_ = f.Close()
	_ = os.Chtimes(CachePath, time.Now(), time.Now())
}

// ttl returns the configured TTL, defaulting to 1h.
// VC_UPDATE_CHECK_TTL_S sets seconds; applies to both vc and cc checks.
func ttl() time.Duration {
	if s := os.Getenv(envTTL); s != "" {
		var secs int
		if _, err := fmt.Sscanf(s, "%d", &secs); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return defaultTTL
}

// compareVersions compares two semver-ish strings (strips leading "v").
// Returns -1 if a < b, 0 if equal, +1 if a > b.
func compareVersions(a, b string) int {
	return cmpVer(parseVer(a), parseVer(b))
}

func parseVer(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	var out [3]int
	for i := range 3 {
		if i < len(parts) {
			fmt.Sscanf(parts[i], "%d", &out[i])
		}
	}
	return out
}

func cmpVer(a, b [3]int) int {
	for i := range 3 {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}
