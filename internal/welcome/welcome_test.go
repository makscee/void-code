package welcome_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/makscee/void-code/internal/welcome"
)

func TestSentinelMissing(t *testing.T) {
	dir := t.TempDir()
	sentinel := filepath.Join(dir, ".welcomed")

	if welcome.SentinelExists(sentinel) {
		t.Fatal("sentinel must not exist before creation")
	}
}

func TestSentinelCreate(t *testing.T) {
	dir := t.TempDir()
	sentinel := filepath.Join(dir, ".welcomed")

	if err := welcome.TouchSentinel(sentinel); err != nil {
		t.Fatalf("TouchSentinel: %v", err)
	}

	if !welcome.SentinelExists(sentinel) {
		t.Fatal("sentinel must exist after Touch")
	}
}

func TestSentinelIdempotent(t *testing.T) {
	dir := t.TempDir()
	sentinel := filepath.Join(dir, ".welcomed")

	if err := welcome.TouchSentinel(sentinel); err != nil {
		t.Fatalf("first Touch: %v", err)
	}
	if err := welcome.TouchSentinel(sentinel); err != nil {
		t.Fatalf("second Touch must be idempotent: %v", err)
	}
}

func TestDefaultSentinelPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	want := filepath.Join(home, ".void-code", ".welcomed")
	got := welcome.DefaultSentinelPath()
	if got != want {
		t.Errorf("DefaultSentinelPath() = %q; want %q", got, want)
	}
}

func TestNeedsWelcome(t *testing.T) {
	dir := t.TempDir()
	sentinel := filepath.Join(dir, ".welcomed")

	// Before touch: needs welcome.
	if !welcome.NeedsWelcome(sentinel) {
		t.Fatal("should need welcome when sentinel absent")
	}

	// After touch: no longer needs welcome.
	_ = welcome.TouchSentinel(sentinel)
	if welcome.NeedsWelcome(sentinel) {
		t.Fatal("should NOT need welcome after sentinel created")
	}
}
