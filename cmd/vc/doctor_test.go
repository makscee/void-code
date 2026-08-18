package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	runErr := fn()
	_ = w.Close()
	os.Stdout = old
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(data), runErr
}

func TestDoctorIsPiOnlyAndDoesNotMutateSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PATH", "")
	settings := filepath.Join(home, ".pi", "agent", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"defaultProvider":"foreign","defaultModel":"foreign"}`), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := captureStdout(t, runDoctor)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Pi runtime") || strings.Contains(strings.ToLower(out), "claude") || strings.Contains(strings.ToLower(out), "codex") {
		t.Fatalf("doctor not Pi-only: %s", out)
	}
	after, _ := os.ReadFile(settings)
	if string(after) != `{"defaultProvider":"foreign","defaultModel":"foreign"}` {
		t.Fatalf("doctor changed Pi settings: %s", after)
	}
}
