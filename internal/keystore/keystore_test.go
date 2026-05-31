package keystore

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// withTempDir points the keystore at a temp dir via the dirFn seam.
func withTempDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	old := dirFn
	dirFn = func() (string, error) { return d, nil }
	t.Cleanup(func() { dirFn = old })
	return d
}

func TestMachineIdentity_CreatesAndPersists(t *testing.T) {
	dir := withTempDir(t)

	id1, err := machineIdentity()
	if err != nil {
		t.Fatalf("machineIdentity: %v", err)
	}
	// .mk file exists, mode 0600 (skip mode assert on Windows).
	mkPath := filepath.Join(dir, ".mk")
	fi, err := os.Stat(mkPath)
	if err != nil {
		t.Fatalf("stat .mk: %v", err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf(".mk mode = %v, want 0600", fi.Mode().Perm())
	}
	// Second call returns the SAME identity (stable across calls).
	id2, err := machineIdentity()
	if err != nil {
		t.Fatalf("machineIdentity 2: %v", err)
	}
	if id1.Recipient().String() != id2.Recipient().String() {
		t.Fatal("machine identity not stable across calls")
	}
}

func TestKeyCRUD_RoundTripAndCiphertext(t *testing.T) {
	dir := withTempDir(t)

	if err := AddKey("openai-personal", "sk-ant-oat01-SECRET-TOKEN"); err != nil {
		t.Fatalf("AddKey: %v", err)
	}
	if err := AddKey("work", "sk-ant-oat01-WORK"); err != nil {
		t.Fatalf("AddKey work: %v", err)
	}

	names, err := ListKeys()
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("ListKeys len = %d, want 2", len(names))
	}

	tok, err := GetKey("openai-personal")
	if err != nil {
		t.Fatalf("GetKey: %v", err)
	}
	if tok != "sk-ant-oat01-SECRET-TOKEN" {
		t.Fatalf("GetKey = %q, want the stored token", tok)
	}

	// File on disk must NOT contain the plaintext token.
	raw, err := os.ReadFile(filepath.Join(dir, "keys.age"))
	if err != nil {
		t.Fatalf("read keys.age: %v", err)
	}
	if bytes.Contains(raw, []byte("SECRET-TOKEN")) {
		t.Fatal("keys.age contains plaintext token — not encrypted")
	}

	if err := DeleteKey("work"); err != nil {
		t.Fatalf("DeleteKey: %v", err)
	}
	names, _ = ListKeys()
	if len(names) != 1 || names[0] != "openai-personal" {
		t.Fatalf("after delete, names = %v, want [openai-personal]", names)
	}
}

func TestListKeys_EmptyWhenAbsent(t *testing.T) {
	withTempDir(t)
	names, err := ListKeys()
	if err != nil {
		t.Fatalf("ListKeys on absent file: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("want empty, got %v", names)
	}
}
