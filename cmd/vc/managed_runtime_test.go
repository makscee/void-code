package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/pibin"
)

// TestRunSpawnNeverExecutesPathPiWithCredentials guards deterministic runtime selection:
// a PATH-controlled pi must never run after VC admits a token-bearing session.
func TestRunSpawnNeverExecutesPathPiWithCredentials(t *testing.T) {
	if testing.Short() {
		t.Skip("executes shell fixtures")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	maliciousDir := filepath.Join(home, "malicious")
	maliciousToken := filepath.Join(home, "malicious-token")
	managedToken := filepath.Join(home, "managed-token")
	if err := os.MkdirAll(maliciousDir, 0700); err != nil {
		t.Fatal(err)
	}
	writeExecutableFixture(t, filepath.Join(maliciousDir, piPathLookupName()), tokenRecorderScript(t, maliciousToken))

	managedPi := managedPiFixturePath(home)
	if err := os.MkdirAll(filepath.Dir(managedPi), 0700); err != nil {
		t.Fatal(err)
	}
	writeExecutableFixture(t, managedPi, tokenRecorderScript(t, managedToken))
	t.Setenv("PATH", maliciousDir)
	assertManagedPiFixtureIsWhatResolverLooksFor(t, home)

	caPath := filepath.Join(home, "relay-ca.pem")
	if err := os.WriteFile(caPath, []byte("test CA"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VC_RELAY_CA", caPath)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"userId":"u1","email":"u@example.test"}`))
	}))
	defer server.Close()
	t.Setenv("VC_AUTH_HOST", server.URL)
	t.Setenv("VC_ACCESS_CHECK_HOST", server.URL)
	if err := auth.Save("admitted-token"); err != nil {
		t.Fatal(err)
	}

	if err := runSpawn(nil, nil); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(maliciousToken); err == nil {
		t.Fatalf("PATH pi ran and received VC_AUTH_TOKEN=%q", data)
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(managedToken); err != nil || recordedToken(data) != "admitted-token" {
		t.Fatalf("managed Pi did not receive admitted token: data=%q err=%v", data, err)
	}
}

// managedPiFixturePath is where this platform's resolver looks for the managed
// Pi entrypoint, and therefore where the fixture has to be planted.
//
// It mirrors internal/pibin.managedPiPathForOS, which is unexported: npm's
// Windows package entrypoint is the generated .cmd shim under node_modules\.bin,
// while unix launches the package's cli.js directly. A single unix-shaped
// fixture is why this test failed on Windows with "GetFileAttributesEx
// ...\runtime\pi\node_modules\.bin: The system cannot find the file specified"
// — the resolver was looking in a directory the fixture never created.
//
// The duplication is checked rather than trusted: see
// assertManagedPiFixtureIsWhatResolverLooksFor.
func managedPiFixturePath(home string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(home, ".void-code", "runtime", "pi", "node_modules", ".bin", "pi.cmd")
	}
	return filepath.Join(home, ".void-code", "runtime", "pi", "node_modules", "@earendil-works", "pi-coding-agent", "dist", "cli.js")
}

// assertManagedPiFixtureIsWhatResolverLooksFor fails at the fixture, with the
// path in the message, rather than letting a stale layout surface as a resolver
// error from inside runSpawn.
//
// It deliberately claims only that the resolver finds something: a resolver
// mutated to consult PATH also finds something, and catching that is the
// business of the malicious-token assertion further down, which says why it
// matters. Resolve canonicalizes the home directory first — on macOS t.TempDir()
// lives under /var, a symlink to /private/var — so the expectation is built on
// the canonical home, the same correction internal/pibin/pibin_test.go carries.
func assertManagedPiFixtureIsWhatResolverLooksFor(t *testing.T, home string) {
	t.Helper()
	canonicalHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pibin.Resolve(); err != nil {
		t.Fatalf("the managed Pi fixture is not what this platform's resolver looks for: %v\nfixture planted at %s", err, managedPiFixturePath(canonicalHome))
	}
}

// piPathLookupName is the file name a PATH lookup for Pi would actually find
// here. Windows resolves only names carrying a PATHEXT extension, so an
// extension-less "pi" would be invisible to exec.LookPath and the hostile half
// of this test would prove nothing on the platform it ran on.
func piPathLookupName() string {
	if runtime.GOOS == "windows" {
		return "pi.cmd"
	}
	return "pi"
}

// tokenRecorderScript returns a script that copies VC_AUTH_TOKEN into sink,
// written in the dialect the platform actually executes: a POSIX shell script
// for the cli.js entrypoint, a batch file for the .cmd shim, which
// harness.Spawn routes through cmd.exe.
func tokenRecorderScript(t *testing.T, sink string) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		return "#!/bin/sh\nprintf %s \"$VC_AUTH_TOKEN\" > " + shellQuote(sink) + "\n"
	}
	// cmd.exe has no escape for these inside a quoted token; a temp directory
	// never contains them, and a fixture that silently mis-redirected would look
	// like "the child never ran".
	if strings.ContainsAny(sink, "\"%") {
		t.Fatalf("sink path %q cannot be quoted for cmd.exe", sink)
	}
	return "@echo off\r\n> \"" + sink + "\" echo %VC_AUTH_TOKEN%\r\n"
}

// recordedToken normalizes what the fixture wrote. The POSIX fixture uses
// printf and writes the token with nothing around it; batch echo has no
// no-newline form worth relying on and appends CRLF. The claim under test is
// which token reached the child, so the trailing line ending is dropped rather
// than asserted.
func recordedToken(data []byte) string { return strings.TrimRight(string(data), "\r\n") }

func writeExecutableFixture(t *testing.T, path, source string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(source), 0700); err != nil {
		t.Fatal(err)
	}
}

func shellQuote(value string) string { return "'" + value + "'" }
