package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/makscee/void-code/internal/auth"
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

func TestSuccessiveDeviceLoginKeepsOnlyNewAuthorization(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldToken, newToken := "old-session.old-secret", "new-session.new-secret"
	if err := auth.Save(oldToken); err != nil {
		t.Fatal(err)
	}
	active := map[string]bool{oldToken: true, newToken: true}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/public/session/revoke" || request.Method != http.MethodPost {
			http.NotFound(writer, request)
			return
		}
		credential := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
		if !active[credential] {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		if stored, _, err := auth.Load(); err != nil || stored != newToken {
			t.Errorf("prior authorization revoked before new credential was durable")
		}
		delete(active, credential)
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	var warnings bytes.Buffer
	if err := installDeviceCredential(server.URL, newToken, server.Client(), &warnings); err != nil {
		t.Fatal(err)
	}
	stored, _, err := auth.Load()
	if err != nil || stored != newToken {
		t.Fatalf("new credential not durable: err=%v", err)
	}
	if len(active) != 1 || !active[newToken] {
		t.Fatalf("active authorizations=%d; want only the new authorization", len(active))
	}
	if warnings.Len() != 0 {
		t.Fatalf("unexpected cleanup warning: %q", warnings.String())
	}
}

func TestPriorAuthorizationCleanupFailureWarnsWithoutFailingLogin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldToken, newToken := "prior-session.prior-secret", "current-session.current-secret"
	if err := auth.Save(oldToken); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusServiceUnavailable) }))
	defer server.Close()
	var warnings bytes.Buffer
	if err := installDeviceCredential(server.URL, newToken, server.Client(), &warnings); err != nil {
		t.Fatal(err)
	}
	stored, _, err := auth.Load()
	if err != nil || stored != newToken {
		t.Fatalf("new credential not durable: err=%v", err)
	}
	if warnings.String() != priorSessionCleanupWarning+"\n" {
		t.Fatalf("warning=%q", warnings.String())
	}
	if strings.Contains(warnings.String(), oldToken) || strings.Contains(warnings.String(), newToken) {
		t.Fatal("cleanup warning exposed a credential")
	}
}

func TestCredentialPersistenceFailureKeepsPriorAuthorization(t *testing.T) {
	oldToken := "prior-session.prior-secret"
	stored := oldToken
	revoked := false
	err := replaceDeviceCredential("replacement-session.replacement-secret", &bytes.Buffer{},
		func() (string, bool, error) { return stored, false, nil },
		func(string) error { return os.ErrPermission },
		func(string) error { revoked = true; return nil },
	)
	if err == nil {
		t.Fatal("expected persistence failure")
	}
	if stored != oldToken {
		t.Fatal("prior credential changed after persistence failure")
	}
	if revoked {
		t.Fatal("prior authorization revoked before replacement was durable")
	}
}
