package harness_test

import (
	"context"
	"os"
	"testing"

	"github.com/makscee/void-code/internal/harness"
)

// TestSpawnMissingBinary verifies that an error is returned when the binary
// cannot be found in PATH — no panic, clean error message.
func TestSpawnMissingBinary(t *testing.T) {
	err := harness.Spawn(context.Background(), "__void_code_nonexistent_bin__", nil, os.Environ())
	if err == nil {
		t.Fatal("expected error for missing binary, got nil")
	}
}

// TestSpawnEcho runs `echo` (always in PATH on supported platforms) and checks
// that Spawn returns nil on success.
func TestSpawnEcho(t *testing.T) {
	if os.Getenv("CI") != "" {
		// Skip noisy process-spawn on CI where echo path may differ.
		t.Skip("skipping echo spawn on CI")
	}
	err := harness.Spawn(context.Background(), "echo", []string{"void-code-test"}, os.Environ())
	if err != nil {
		t.Fatalf("unexpected error from echo: %v", err)
	}
}
