//go:build vctest

// vctest is a test helper binary for VCD-58 evidence collection.
// It sets up a keystore in the given HOME dir and optionally exercises delete operations.
// Build with: go build -tags vctest -o /tmp/vctest ./cmd/vctest/
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"bytes"

	"github.com/makscee/void-code/internal/keystore"
	"github.com/makscee/void-code/internal/provider"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: vctest <command> [args...]")
		fmt.Fprintln(os.Stderr, "commands: setup-keys <home>, delete-key <home> <name>, list-keys <home>, set-active <home> <provider>")
		os.Exit(1)
	}
	switch os.Args[1] {
	case "setup-keys":
		home := os.Args[2]
		os.Setenv("HOME", home)
		os.Setenv("USERPROFILE", home)
		addKey("test-key-alpha", "sk-ant-oat01-ALPHA")
		addKey("test-key-beta", "sk-ant-oat01-BETA")
		listKeys()
		verifyEncrypted(home)
	case "delete-key":
		home := os.Args[2]
		name := os.Args[3]
		os.Setenv("HOME", home)
		os.Setenv("USERPROFILE", home)
		if err := keystore.DeleteKey(name); err != nil {
			fmt.Fprintf(os.Stderr, "DeleteKey %q: %v\n", name, err)
			os.Exit(1)
		}
		fmt.Printf("Deleted key: %q\n", name)
		listKeys()
		verifyEncrypted(home)
	case "set-active":
		home := os.Args[2]
		provStr := os.Args[3]
		os.Setenv("HOME", home)
		os.Setenv("USERPROFILE", home)
		p := provider.Parse(provStr)
		if err := provider.Save(p); err != nil {
			fmt.Fprintf(os.Stderr, "provider.Save: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Active provider set to: %s\n", p.Label())
	case "list-keys":
		home := os.Args[2]
		os.Setenv("HOME", home)
		os.Setenv("USERPROFILE", home)
		listKeys()
		p := provider.Load()
		fmt.Printf("Active provider: %s\n", p.Label())
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func addKey(name, token string) {
	if err := keystore.AddKey(name, token); err != nil {
		fmt.Fprintf(os.Stderr, "AddKey %q: %v\n", name, err)
		os.Exit(1)
	}
	fmt.Printf("Added key: %q\n", name)
}

func listKeys() {
	names, err := keystore.ListKeys()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ListKeys: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Keys in store: %v\n", names)
}

func verifyEncrypted(home string) {
	keysAge := filepath.Join(home, ".void-code", "keys.age")
	data, err := os.ReadFile(keysAge)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read keys.age: %v\n", err)
		return
	}
	// Check that no test token appears in plaintext
	encrypted := !bytes.Contains(data, []byte("sk-ant-oat01-")) 
	fmt.Printf("keys.age: %d bytes, encrypted (no plaintext tokens): %v\n", len(data), encrypted)
}
