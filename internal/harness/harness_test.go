package harness_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
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

// TestSpawnEnvInjection uses the `env` utility (available on Unix/macOS and
// present as cmd.exe on Windows via PATH) to verify that the env slice passed
// to Spawn is actually received by the child process.
//
// We spawn `env` with a custom env that includes a canary variable, then
// capture stdout and assert the canary appears.
func TestSpawnEnvInjection(t *testing.T) {
	// Find the `env` binary; skip if unavailable (unusual systems).
	envBin, err := exec.LookPath("env")
	if err != nil {
		t.Skip("env binary not in PATH, skipping env injection test")
	}

	// Build a clean env with a canary value.
	childEnv := []string{
		"PATH=" + os.Getenv("PATH"),
		"VC_CANARY_KEY=relay-injected-value",
		"HTTPS_PROXY=http://relay.makscee.ru:8448",
		"NODE_EXTRA_CA_CERTS=/tmp/test-ca.pem",
		"ANTHROPIC_API_KEY=",
		"ANTHROPIC_BASE_URL=",
		"ANTHROPIC_AUTH_TOKEN=test-token-xyz",
	}

	// Redirect stdout so we can capture it.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origStdout := os.Stdout
	os.Stdout = w

	spawnErr := harness.Spawn(context.Background(), envBin, nil, childEnv)

	w.Close()
	os.Stdout = origStdout

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	r.Close()

	if spawnErr != nil {
		t.Fatalf("Spawn error: %v", spawnErr)
	}

	output := buf.String()
	if !strings.Contains(output, "VC_CANARY_KEY=relay-injected-value") {
		t.Errorf("canary not found in child env output:\n%s", output)
	}
	if !strings.Contains(output, "HTTPS_PROXY=http://relay.makscee.ru:8448") {
		t.Errorf("HTTPS_PROXY not found in child env output:\n%s", output)
	}
	if !strings.Contains(output, "ANTHROPIC_AUTH_TOKEN=test-token-xyz") {
		t.Errorf("ANTHROPIC_AUTH_TOKEN not found in child env output:\n%s", output)
	}
}

// TestSpawnExitCode verifies that Spawn propagates a non-zero exit code as
// *exec.ExitError so callers can extract the numeric code.
func TestSpawnExitCode(t *testing.T) {
	// Use `false` (always exits 1) if available; fallback to sh -c "exit 2".
	var bin string
	var args []string
	if p, err := exec.LookPath("false"); err == nil {
		bin = p
	} else if p, err := exec.LookPath("sh"); err == nil {
		bin = p
		args = []string{"-c", "exit 2"}
	} else {
		t.Skip("neither false nor sh available")
	}

	err := harness.Spawn(context.Background(), bin, args, os.Environ())
	if err == nil {
		t.Fatal("expected non-zero exit error, got nil")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected *exec.ExitError, got %T: %v", err, err)
	}
	if exitErr.ExitCode() == 0 {
		t.Errorf("exit code should be non-zero, got %d", exitErr.ExitCode())
	}
}
