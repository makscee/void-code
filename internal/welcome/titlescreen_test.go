// package welcome (internal test) — can access unexported model.View()
package welcome

import (
	"strings"
	"testing"
)

// TestView_ShowsActiveProvider verifies that the main title screen (menuView)
// includes the active provider label so the user can see which provider is
// selected without navigating to the Providers sub-menu.
//
// Item 2 of VCD-57: "display the currently-active provider on the title/welcome
// screen (not only inside the Providers submenu)".
func TestView_ShowsActiveProvider(t *testing.T) {
	cases := []struct {
		activeProvider string
		wantContains   string
	}{
		{"relay", "Relay (void-relay)"},
		{"plain", "Plain Claude Code"},
		{"key:work", "key: work"},
		{"key:my-key", "key: my-key"},
		// Empty / unset → defaults to Relay
		{"", "Relay (void-relay)"},
	}
	for _, c := range cases {
		cb := Callbacks{ActiveProvider: c.activeProvider}
		m := newModel(AuthState{LoggedIn: true, Identity: "u@x.com"}, cb)
		view := m.View()

		// Strip ANSI for easier matching.
		plain := stripANSIInternal(view)
		if !strings.Contains(plain, c.wantContains) {
			t.Errorf("activeProvider=%q: View() does not contain %q\nView:\n%s",
				c.activeProvider, c.wantContains, plain)
		}
	}
}

// TestView_ShowsActiveProvider_NotLoggedIn verifies that the provider line is
// still shown (as Relay default) even when logged out — the Providers menu is
// accessible regardless of auth state.
func TestView_ShowsActiveProvider_NotLoggedIn(t *testing.T) {
	cb := Callbacks{ActiveProvider: "relay"}
	m := newModel(AuthState{LoggedIn: false}, cb)
	view := m.View()
	plain := stripANSIInternal(view)
	if !strings.Contains(plain, "Relay (void-relay)") {
		t.Errorf("View() for logged-out must still show provider; got:\n%s", plain)
	}
}

// stripANSIInternal is the same ANSI-strip helper as in screenshot_test.go,
// duplicated here to avoid a package-level name collision.
func stripANSIInternal(s string) string {
	var out strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) && (s[i] < '@' || s[i] > '~') {
				i++
			}
			if i < len(s) {
				i++
			}
			continue
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}
