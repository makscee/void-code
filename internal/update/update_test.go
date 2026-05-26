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
