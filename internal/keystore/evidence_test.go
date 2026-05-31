//go:build evidence

package keystore

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/makscee/void-code/internal/provider"
)

// TestEvidence_AddKeyAndReload is a build-tag-gated test that writes to the
// real HOME (set via env) to produce real-path evidence for VCD-57.
// Run with: HOME=/tmp/vc57-evidence go test -tags evidence ./internal/keystore/ -v -run TestEvidence
func TestEvidence_AddKeyAndReload(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || home == "/root" {
		t.Skip("HOME not set to evidence dir")
	}

	// Add a key using the real path.
	if err := AddKey("evidence-key", "sk-ant-oat01-EVID"); err != nil {
		t.Fatalf("AddKey: %v", err)
	}
	fmt.Println("AddKey OK: evidence-key saved")

	// Verify keys.age is NOT plaintext.
	dir, _ := dirFn()
	agePath := filepath.Join(dir, "keys.age")
	raw, err := os.ReadFile(agePath)
	if err != nil {
		t.Fatalf("read keys.age: %v", err)
	}
	if bytes.Contains(raw, []byte("EVID")) {
		t.Fatal("FAIL: keys.age contains plaintext token")
	}
	fmt.Printf("keys.age is encrypted (NOT plaintext): first 32 bytes hex: %x\n", raw[:min(32, len(raw))])

	// Check .mk mode.
	mkPath := filepath.Join(dir, ".mk")
	fi, _ := os.Stat(mkPath)
	fmt.Printf(".mk mode: %v\n", fi.Mode().Perm())

	// Reload — should see the key.
	names, err := ListKeys()
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	fmt.Printf("ListKeys after reload: %v\n", names)

	tok, err := GetKey("evidence-key")
	if err != nil {
		t.Fatalf("GetKey: %v", err)
	}
	if tok != "sk-ant-oat01-EVID" {
		t.Fatalf("GetKey mismatch: %q", tok)
	}
	fmt.Printf("GetKey roundtrip: OK (token matches)\n")

	// Save + reload provider.
	if err := provider.Save(provider.Provider{Kind: provider.NamedKey, Name: "evidence-key"}); err != nil {
		t.Fatalf("provider.Save: %v", err)
	}
	p := provider.Load()
	fmt.Printf("provider.Load: kind=%d name=%q label=%q\n", p.Kind, p.Name, p.Label())
	if p.Name != "evidence-key" {
		t.Fatalf("provider round-trip mismatch: %+v", p)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
