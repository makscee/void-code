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

func TestReleasedDeviceFlowOpensPublicDeviceDeepLink(t *testing.T) {
	verificationURL := deviceBrowserURL("https://auth.makscee.ru/", "/device")
	if verificationURL != "https://auth.makscee.ru/device" {
		t.Fatalf("verification=%q", verificationURL)
	}
	if strings.Contains(verificationURL, "/identity-stage") || strings.Contains(verificationURL, "/login") {
		t.Fatal("released device flow must open the standard device deep link directly")
	}
}

func TestVoidCodeDeviceLabelsArePlatformAwareAndValueFree(t *testing.T) {
	cases := map[string]string{"darwin": "Void Code on macOS", "windows": "Void Code on Windows", "linux": "Void Code on Linux", "plan9": "Void Code on this platform"}
	for goos, want := range cases {
		if got := voidCodeDeviceLabel(goos); got != want {
			t.Fatalf("label(%q)=%q want %q", goos, got, want)
		}
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
