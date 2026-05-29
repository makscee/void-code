package update_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/makscee/void-code/internal/update"
)

// --- semver compare ---

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v0.1.0", "v0.1.1", -1},
		{"v0.1.1", "v0.1.0", 1},
		{"v0.1.0", "v0.1.0", 0},
		{"v1.2.3", "v1.2.4", -1},
		{"v2.0.0", "v1.99.99", 1},
		{"v0.0.1", "v0.0.2", -1},
		{"0.1.0", "0.1.1", -1}, // without "v" prefix
	}
	for _, c := range cases {
		got := update.CompareVersions(c.a, c.b)
		if got != c.want {
			t.Errorf("CompareVersions(%q, %q) = %d; want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestCompareInvalid(t *testing.T) {
	// Non-semver strings should not panic; treat as equal (0).
	got := update.CompareVersions("dev", "dev")
	if got != 0 {
		t.Errorf("dev vs dev should be 0, got %d", got)
	}
}

// --- version.json parse ---

func TestParseVersionJSON(t *testing.T) {
	raw := `{"version":"v0.2.0","artifacts":{"darwin/arm64":"vc-darwin-arm64","darwin/amd64":"vc-darwin-amd64","linux/amd64":"vc-linux-amd64","windows/amd64":"vc-windows-amd64.exe"}}`
	v, err := update.ParseVersionJSON([]byte(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Version != "v0.2.0" {
		t.Errorf("version = %q; want v0.2.0", v.Version)
	}
	if v.Artifacts["darwin/arm64"] != "vc-darwin-arm64" {
		t.Errorf("darwin/arm64 artifact = %q; want vc-darwin-arm64", v.Artifacts["darwin/arm64"])
	}
}

func TestParseVersionJSONInvalid(t *testing.T) {
	_, err := update.ParseVersionJSON([]byte("not-json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// --- PlatformKey ---

func TestPlatformKey(t *testing.T) {
	key := update.PlatformKey()
	// Must be non-empty and contain OS+arch
	if key == "" {
		t.Fatal("PlatformKey must not be empty")
	}
	// On the test host it must resolve to a known pattern
	os_ := runtime.GOOS
	arch := runtime.GOARCH
	if os_ == "darwin" || os_ == "linux" || os_ == "windows" {
		expected := os_ + "/" + arch
		if key != expected {
			t.Errorf("PlatformKey() = %q; want %q", key, expected)
		}
	}
}

// --- integration: CheckAndUpdate with mock server ---

func TestCheckNoUpdate(t *testing.T) {
	// Server reports same version as current → no update.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "version.json") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(update.VersionJSON{
				Version:   "v0.1.0",
				Artifacts: map[string]string{"darwin/arm64": "vc-darwin-arm64"},
			})
		}
	}))
	defer srv.Close()

	updated, err := update.CheckAndUpdate(update.Options{
		Current:    "v0.1.0",
		BaseURL:    srv.URL,
		BinaryPath: "", // not needed — no update
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated {
		t.Error("expected no update when versions match")
	}
}

func TestCheckUpdateAvailable(t *testing.T) {
	// Write a fake binary so atomic swap has something to do.
	tmpDir := t.TempDir()
	binaryPath := filepath.Join(tmpDir, "vc")
	if err := os.WriteFile(binaryPath, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}

	newBinaryContent := []byte("new-binary-content")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "version.json"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(update.VersionJSON{
				Version: "v0.2.0",
				Artifacts: map[string]string{
					update.PlatformKey(): "vc-" + update.PlatformKey(),
				},
			})
		default:
			// Serve the fake binary for any other path.
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(newBinaryContent)
		}
	}))
	defer srv.Close()

	updated, err := update.CheckAndUpdate(update.Options{
		Current:    "v0.1.0",
		BaseURL:    srv.URL,
		BinaryPath: binaryPath,
	})
	if err != nil {
		t.Fatalf("update error: %v", err)
	}
	if !updated {
		t.Fatal("expected update to happen")
	}

	// Binary should be replaced.
	got, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(newBinaryContent) {
		t.Errorf("binary content = %q; want %q", string(got), string(newBinaryContent))
	}
}

func TestCheckUpdateMissingPlatform(t *testing.T) {
	// version.json doesn't have a file for our platform → return error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "version.json") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"version":"v0.2.0","artifacts":{}}`)
		}
	}))
	defer srv.Close()

	_, err := update.CheckAndUpdate(update.Options{
		Current:    "v0.1.0",
		BaseURL:    srv.URL,
		BinaryPath: "",
	})
	if err == nil {
		t.Fatal("expected error when platform binary not found in release")
	}
}

func TestCheckUpdateServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := update.CheckAndUpdate(update.Options{
		Current:    "v0.1.0",
		BaseURL:    srv.URL,
		BinaryPath: "",
	})
	if err == nil {
		t.Fatal("expected error on server 500")
	}
}

// --- legacy hyphen key fallback (regression for darwin-arm64 bug) ---

func TestCheckUpdateLegacyHyphenKey(t *testing.T) {
	// version.json only has hyphen-style keys (pre-v0.1.3 releases).
	// Client must still resolve the artifact.
	tmpDir := t.TempDir()
	binaryPath := filepath.Join(tmpDir, "vc")
	if err := os.WriteFile(binaryPath, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	newBinaryContent := []byte("new-bin")

	legacyKey := update.PlatformKeyLegacy() // e.g. "darwin-arm64"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "version.json"):
			w.Header().Set("Content-Type", "application/json")
			// Only hyphen keys — no slash keys — simulating a pre-v0.1.3 release.
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"version":   "v0.2.0",
				"artifacts": map[string]string{legacyKey: "vc-" + legacyKey},
			})
		default:
			_, _ = w.Write(newBinaryContent)
		}
	}))
	defer srv.Close()

	updated, err := update.CheckAndUpdate(update.Options{
		Current:    "v0.1.0",
		BaseURL:    srv.URL,
		BinaryPath: binaryPath,
	})
	if err != nil {
		t.Fatalf("unexpected error with legacy hyphen key: %v", err)
	}
	if !updated {
		t.Fatal("expected update when legacy hyphen key present")
	}
}

// TestCheckUpdateBothKeys verifies that a version.json with BOTH slash and
// hyphen keys (v0.1.3+ format) is handled correctly — slash key takes precedence.
func TestCheckUpdateBothKeys(t *testing.T) {
	tmpDir := t.TempDir()
	binaryPath := filepath.Join(tmpDir, "vc")
	if err := os.WriteFile(binaryPath, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	newBinaryContent := []byte("new-bin")

	slashKey := update.PlatformKey()     // e.g. "darwin/arm64"
	legacyKey := update.PlatformKeyLegacy() // e.g. "darwin-arm64"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "version.json"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"version": "v0.2.0",
				"artifacts": map[string]string{
					slashKey:  "vc-slash",
					legacyKey: "vc-hyphen",
				},
			})
		case strings.HasSuffix(r.URL.Path, "vc-slash"):
			_, _ = w.Write(newBinaryContent)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	updated, err := update.CheckAndUpdate(update.Options{
		Current:    "v0.1.0",
		BaseURL:    srv.URL,
		BinaryPath: binaryPath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !updated {
		t.Fatal("expected update")
	}
}

// --- ProbeAsync ---

func TestProbeAsyncHasUpdate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "version.json") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"version":   "0.9.0",
				"artifacts": map[string]string{update.PlatformKey(): "vc-x"},
			})
		}
	}))
	defer srv.Close()

	ch := update.ProbeAsync("v0.1.0", srv.URL, 5*time.Second)
	result := <-ch
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if !result.HasUpdate {
		t.Fatal("expected HasUpdate=true")
	}
	if result.Latest != "v0.9.0" {
		t.Errorf("Latest = %q; want v0.9.0", result.Latest)
	}
}

func TestProbeAsyncNoUpdate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "version.json") {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"version":   "0.1.0",
				"artifacts": map[string]string{},
			})
		}
	}))
	defer srv.Close()

	ch := update.ProbeAsync("v0.1.0", srv.URL, 5*time.Second)
	result := <-ch
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if result.HasUpdate {
		t.Fatal("expected HasUpdate=false when at latest")
	}
}

func TestProbeAsyncOffline(t *testing.T) {
	// Point at a non-existent server to simulate offline.
	ch := update.ProbeAsync("v0.1.0", "http://127.0.0.1:1", 500*time.Millisecond)
	result := <-ch
	if result.Err == nil {
		t.Fatal("expected error when server unreachable")
	}
	if result.HasUpdate {
		t.Fatal("HasUpdate must be false on error")
	}
}

func TestProbeAsyncTimeout(t *testing.T) {
	// Server hangs — probe must return within timeout.
	hung := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-hung // block forever
	}))
	defer srv.Close()
	defer close(hung)

	start := time.Now()
	ch := update.ProbeAsync("v0.1.0", srv.URL, 200*time.Millisecond)
	result := <-ch
	elapsed := time.Since(start)

	if result.Err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed > 2*time.Second {
		t.Errorf("timeout took too long: %v", elapsed)
	}
}

// --- PlatformKeyLegacy ---

func TestPlatformKeyLegacy(t *testing.T) {
	key := update.PlatformKeyLegacy()
	os_ := runtime.GOOS
	arch := runtime.GOARCH
	expected := os_ + "-" + arch
	if key != expected {
		t.Errorf("PlatformKeyLegacy() = %q; want %q", key, expected)
	}
}

// --- regression: darwin-arm64 lookup ---

// TestDarwinArm64Regression verifies that a version.json with ONLY the
// "darwin-arm64" hyphen key (as produced by releases before v0.1.3) is
// handled by the current client.  This is the exact failure scenario reported
// by the operator.
func TestDarwinArm64Regression(t *testing.T) {
	tmpDir := t.TempDir()
	binaryPath := filepath.Join(tmpDir, "vc")
	if err := os.WriteFile(binaryPath, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	newBinaryContent := []byte("fixed-binary")

	allSixHyphenKeys := map[string]string{
		"darwin-amd64":  "vc-darwin-amd64",
		"darwin-arm64":  "vc-darwin-arm64",
		"linux-amd64":   "vc-linux-amd64",
		"linux-arm64":   "vc-linux-arm64",
		"windows-amd64": "vc-windows-amd64.exe",
		"windows-arm64": "vc-windows-arm64.exe",
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "version.json"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"version":   "0.1.2",
				"artifacts": allSixHyphenKeys,
			})
		default:
			_, _ = w.Write(newBinaryContent)
		}
	}))
	defer srv.Close()

	updated, err := update.CheckAndUpdate(update.Options{
		Current:    "v0.1.1",
		BaseURL:    srv.URL,
		BinaryPath: binaryPath,
	})
	if err != nil {
		t.Fatalf("darwin-arm64 regression: unexpected error: %v", err)
	}
	if !updated {
		t.Fatal("darwin-arm64 regression: expected update to succeed")
	}
}

// TestCheckUpdateBinPrefixArtifacts verifies that artifact paths with a "bin/"
// prefix (as served from auth.makscee.ru/vc) resolve correctly.
// With baseURL="https://host/vc", artifact="bin/vc-darwin-arm64",
// the binary URL becomes "https://host/vc/bin/vc-darwin-arm64" which is
// where void-auth serves the file.
func TestCheckUpdateBinPrefixArtifacts(t *testing.T) {
	tmpDir := t.TempDir()
	binaryPath := filepath.Join(tmpDir, "vc")
	if err := os.WriteFile(binaryPath, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	newBinaryContent := []byte("new-bin-via-bin-prefix")

	slashKey := update.PlatformKey() // e.g. "darwin/arm64"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "version.json"):
			w.Header().Set("Content-Type", "application/json")
			// Artifact paths with bin/ prefix — mirrors void-auth layout.
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"version": "v0.2.0",
				"artifacts": map[string]string{
					slashKey: "bin/vc-" + runtime.GOOS + "-" + runtime.GOARCH,
				},
			})
		case strings.HasPrefix(r.URL.Path, "/bin/"):
			_, _ = w.Write(newBinaryContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	updated, err := update.CheckAndUpdate(update.Options{
		Current:    "v0.1.0",
		BaseURL:    srv.URL,
		BinaryPath: binaryPath,
	})
	if err != nil {
		t.Fatalf("bin-prefix artifact: unexpected error: %v", err)
	}
	if !updated {
		t.Fatal("bin-prefix artifact: expected update to succeed")
	}
	got, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(newBinaryContent) {
		t.Errorf("binary content = %q; want %q", string(got), string(newBinaryContent))
	}
}

// TestDefaultReleaseBaseURL verifies the default URL points to auth.makscee.ru/vc.
func TestDefaultReleaseBaseURL(t *testing.T) {
	const want = "https://auth.makscee.ru/vc"
	if update.DefaultReleaseBaseURL != want {
		t.Errorf("DefaultReleaseBaseURL = %q; want %q", update.DefaultReleaseBaseURL, want)
	}
}

// TestCheckUpdateWindowsExeExtension verifies that the Windows artifact key
// ("windows/amd64") resolves to a .exe filename in a standard version.json
// and that CheckAndUpdate replaces the binary successfully (platform-neutral
// mechanics test — does not require a Windows host).
func TestCheckUpdateWindowsExeExtension(t *testing.T) {
	tmpDir := t.TempDir()
	// Use a .exe extension to mirror the real Windows deployment path.
	binaryPath := filepath.Join(tmpDir, "vc.exe")
	if err := os.WriteFile(binaryPath, []byte("old-win-bin"), 0755); err != nil {
		t.Fatal(err)
	}
	newContent := []byte("new-win-bin")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "version.json"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"version": "v0.1.11",
				"artifacts": map[string]string{
					update.PlatformKey(): "vc-" + runtime.GOOS + "-" + runtime.GOARCH + ".exe",
				},
			})
		default:
			_, _ = w.Write(newContent)
		}
	}))
	defer srv.Close()

	updated, err := update.CheckAndUpdate(update.Options{
		Current:    "v0.1.10",
		BaseURL:    srv.URL,
		BinaryPath: binaryPath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !updated {
		t.Fatal("expected update to succeed")
	}
	got, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatalf("read updated binary: %v", err)
	}
	if string(got) != string(newContent) {
		t.Errorf("binary content = %q; want %q", got, newContent)
	}
}

// TestCleanOldBinaryNoop verifies that CleanOldBinary is safe to call when no
// .old file exists (must not panic or error).
func TestCleanOldBinaryNoop(t *testing.T) {
	// CleanOldBinary uses os.Executable() which points to the test binary.
	// There is no test-binary.old file, so this must be a no-op.
	update.CleanOldBinary()
	// Success = no panic.
}
