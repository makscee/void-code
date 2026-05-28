package ccupdate

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// --- compareVersions ---

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"v1.0.0", "1.0.0", 0},
		{"1.0.0", "v1.0.0", 0},
		{"1.2.3", "1.2.4", -1},
		{"1.2.4", "1.2.3", 1},
		{"2.0.0", "1.9.9", 1},
		{"0.9.0", "1.0.0", -1},
		{"1.10.0", "1.9.0", 1},
	}
	for _, c := range cases {
		got := compareVersions(c.a, c.b)
		if got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d; want %d", c.a, c.b, got, c.want)
		}
	}
}

// --- ttl ---

func TestTTLDefault(t *testing.T) {
	os.Unsetenv(envTTL)
	if ttl() != time.Hour {
		t.Errorf("default TTL = %v; want 1h", ttl())
	}
}

func TestTTLFromEnv(t *testing.T) {
	t.Setenv(envTTL, "120")
	if ttl() != 2*time.Minute {
		t.Errorf("TTL from env = %v; want 2m", ttl())
	}
}

func TestTTLInvalidEnv(t *testing.T) {
	t.Setenv(envTTL, "notanumber")
	if ttl() != time.Hour {
		t.Errorf("invalid env TTL = %v; want 1h (default)", ttl())
	}
}

// --- cacheFresh / touchCache ---

func TestCacheNotFreshWhenMissing(t *testing.T) {
	tmp := t.TempDir()
	CachePath = filepath.Join(tmp, "cc-sentinel")
	defer func() { CachePath = "" }()

	if cacheFresh() {
		t.Error("expected cache not fresh when sentinel missing")
	}
}

func TestCacheFreshAfterTouch(t *testing.T) {
	tmp := t.TempDir()
	CachePath = filepath.Join(tmp, "cc-sentinel")
	defer func() { CachePath = "" }()

	touchCache()
	if !cacheFresh() {
		t.Error("expected cache fresh after touch")
	}
}

func TestCacheStaleAfterTTL(t *testing.T) {
	tmp := t.TempDir()
	CachePath = filepath.Join(tmp, "cc-sentinel")
	defer func() { CachePath = "" }()

	// Create sentinel with mtime 2h ago.
	if err := os.WriteFile(CachePath, nil, 0600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(CachePath, past, past); err != nil {
		t.Fatal(err)
	}

	t.Setenv(envTTL, "3600") // 1h
	if cacheFresh() {
		t.Error("expected cache stale when sentinel is 2h old and TTL is 1h")
	}
}

// --- npmBin ---

func TestNpmBinNotEmpty(t *testing.T) {
	bin := npmBin()
	if bin == "" {
		t.Error("npmBin() returned empty string")
	}
}

// --- isHardError ---

func TestIsHardError(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"", false},
		{"timeout", false},
		{"connection refused", false},
		{"npm view: 404 Not Found", true},
		{"npm view: 403 Forbidden", true},
		{"npm view: 500 Internal Server Error", true},
		{"E404 Not Found", true},
	}
	for _, c := range cases {
		var err error
		if c.msg != "" {
			err = &stubError{c.msg}
		}
		got := isHardError(err)
		if got != c.want {
			t.Errorf("isHardError(%q) = %v; want %v", c.msg, got, c.want)
		}
	}
}

type stubError struct{ msg string }

func (e *stubError) Error() string { return e.msg }

// --- compareVersions edge cases ---

func TestCompareVersionsEmpty(t *testing.T) {
	// Empty string should be treated as 0.0.0
	if compareVersions("", "1.0.0") >= 0 {
		t.Error("empty version should be less than 1.0.0")
	}
	if compareVersions("1.0.0", "") <= 0 {
		t.Error("1.0.0 should be greater than empty")
	}
}
