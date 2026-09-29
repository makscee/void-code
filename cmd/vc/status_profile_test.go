package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/config"
)

// A client sees their balance, weekly limit and usage on the profile page, so
// `vc status` points there in every state, and --json carries the same URL for
// the desktop (void-board#480).
func TestStatusShowsProfileLinkInEveryState(t *testing.T) {
	const line = "Профиль: https://profile.makscee.ru/profile"
	for _, tc := range []struct {
		name  string
		token string
		code  int
		body  string
		state string
	}{
		{"signed out", "", 0, "", "signed_out"},
		{"verification failed", "some-token", http.StatusUnauthorized, "", "invalid_credential"},
		{"signed in", "some-token", http.StatusOK, `{"userId":"u","email":"a@example.test"}`, "signed_in"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.code)
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			t.Setenv("VC_AUTH_HOST", srv.URL)
			t.Setenv("VC_ACCESS_CHECK_HOST", srv.URL)
			if tc.token != "" {
				if err := auth.Save(tc.token); err != nil {
					t.Fatal(err)
				}
			}
			out, err := captureStdout(t, func() error { return runStatus(nil, nil) })
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(ansiEscape.ReplaceAllString(out, "")), "\n")
			if last := lines[len(lines)-1]; last != line {
				t.Errorf("last line = %q, want %q (output: %s)", last, line, out)
			}

			var buf bytes.Buffer
			if err := runStatusJSON(config.OSResolve(), &buf); err != nil {
				t.Fatal(err)
			}
			obj := decodeSingleJSONObject(t, buf.Bytes())
			if obj["authState"] != tc.state {
				t.Fatalf("authState = %v, want %s", obj["authState"], tc.state)
			}
			if obj["profileUrl"] != "https://profile.makscee.ru/profile" {
				t.Errorf("profileUrl = %v, want https://profile.makscee.ru/profile", obj["profileUrl"])
			}
		})
	}
}
