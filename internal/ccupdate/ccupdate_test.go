package ccupdate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/makscee/void-code/internal/update"
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
		got := update.CompareVersions(c.a, c.b)
		if got != c.want {
			t.Errorf("CompareVersions(%q, %q) = %d; want %d", c.a, c.b, got, c.want)
		}
	}
}

// oldCompareVersions is a verbatim copy of ccupdate's former private comparator
// (parseVer via fmt.Sscanf("%d") + cmpVer), removed when CheckAndUpdate was
// consolidated onto update.CompareVersions. It is kept here ONLY to pin that the
// consolidation is behavior-preserving on ccupdate's real input domain.
func oldCompareVersions(a, b string) int {
	parse := func(v string) [3]int {
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
	an, bn := parse(a), parse(b)
	for i := range 3 {
		if an[i] < bn[i] {
			return -1
		}
		if an[i] > bn[i] {
			return 1
		}
	}
	return 0
}

// TestCompareVersionsEquivalence proves the swap from the old private comparator
// to update.CompareVersions is behavior-preserving on ccupdate's ACTUAL input
// domain. Both comparison operands (installed via `npm list -g`, latest via
// `npm view ... version`) are versions published to the @anthropic-ai/claude-code
// npm package, which only ever emits plain X.Y.Z semver (verified: all 441
// published versions match ^\d+\.\d+\.\d+$, zero pre-release/build tags). The two
// comparators diverge only on non-numeric segments, which never reach this code.
func TestCompareVersionsEquivalence(t *testing.T) {
	domain := []string{
		"1.0.0", "1.0.1", "1.2.3", "1.2.4", "1.9.0", "1.10.0",
		"2.0.0", "2.1.170", "2.1.183", "0.9.0", "10.0.0", "1.0.10",
		"v1.2.3", "v2.1.183", "",
	}
	for _, a := range domain {
		for _, b := range domain {
			want := oldCompareVersions(a, b)
			got := update.CompareVersions(a, b)
			if got != want {
				t.Errorf("divergence on plain-semver domain: CompareVersions(%q, %q) = %d; old = %d", a, b, got, want)
			}
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
	if update.CompareVersions("", "1.0.0") >= 0 {
		t.Error("empty version should be less than 1.0.0")
	}
	if update.CompareVersions("1.0.0", "") <= 0 {
		t.Error("1.0.0 should be greater than empty")
	}
}
