package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoginExposesOnlyDeviceAuthorization(t *testing.T) {
	if loginCmd.Flags().Lookup("email") != nil || loginCmd.Flags().Lookup("code") != nil || loginCmd.Flags().Lookup("device") != nil {
		t.Fatal("legacy login flags must not be exposed")
	}
	if loginCmd.Short == "" {
		t.Fatal("login help is empty")
	}
}

func TestReleasedDeviceFlowUsesPublicIdentityBrowserRoutes(t *testing.T) {
	loginURL, verificationURL := deviceBrowserURLs("https://auth.makscee.ru/", "/device")
	if loginURL != "https://auth.makscee.ru/login" || verificationURL != "https://auth.makscee.ru/device" {
		t.Fatalf("login=%q verification=%q", loginURL, verificationURL)
	}
	if strings.Contains(loginURL, "/identity-stage") || strings.Contains(verificationURL, "/identity-stage") {
		t.Fatal("released device flow must not expose staging routes")
	}
}

func TestLegacyCredentialPrintsValueFreeMigrationInstruction(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	legacyDir := filepath.Join(home, ".claudev")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(legacyDir, "token")
	legacyValue := "legacy-secret-must-not-appear"
	if err := os.WriteFile(legacyPath, []byte(legacyValue), 0o600); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	printLegacyMigrationInstruction(&output)
	if got := output.String(); got != legacyMigrationInstruction+"\n" {
		t.Fatalf("instruction = %q", got)
	}
	if bytes.Contains(output.Bytes(), []byte(legacyValue)) {
		t.Fatal("instruction exposed the legacy credential")
	}
	if got, err := os.ReadFile(legacyPath); err != nil || string(got) != legacyValue {
		t.Fatalf("legacy credential changed: value=%q err=%v", got, err)
	}
}
