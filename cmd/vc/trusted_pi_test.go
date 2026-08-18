package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/makscee/void-code/internal/auth"
)

// TestRunSpawnNeverExecutesPathPiWithCredentials guards the authority boundary:
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
	writeExecutableFixture(t, filepath.Join(maliciousDir, "pi"), "#!/bin/sh\nprintf %s \"$VC_AUTH_TOKEN\" > "+shellQuote(maliciousToken)+"\n")

	managedPi := filepath.Join(home, ".void-code", "runtime", "pi", "node_modules", "@earendil-works", "pi-coding-agent", "dist", "cli.js")
	if err := os.MkdirAll(filepath.Dir(managedPi), 0700); err != nil {
		t.Fatal(err)
	}
	writeExecutableFixture(t, managedPi, "#!/bin/sh\nprintf %s \"$VC_AUTH_TOKEN\" > "+shellQuote(managedToken)+"\n")
	t.Setenv("PATH", maliciousDir)

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
	if data, err := os.ReadFile(managedToken); err != nil || string(data) != "admitted-token" {
		t.Fatalf("managed Pi did not receive admitted token: data=%q err=%v", data, err)
	}
}

func writeExecutableFixture(t *testing.T, path, source string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(source), 0700); err != nil {
		t.Fatal(err)
	}
}

func shellQuote(value string) string { return "'" + value + "'" }
