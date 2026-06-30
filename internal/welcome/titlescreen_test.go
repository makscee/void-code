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
		{"relay", "DeepSeek relay"},
		{"plain", "Plain harness run"},
		{"key:work", "key: work"},
		{"key:my-key", "key: my-key"},
		// Empty / unset → defaults to Relay
		{"", "DeepSeek relay"},
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

func TestView_LoggedOutShowsLoginOnly(t *testing.T) {
	m := newModel(AuthState{LoggedIn: false}, Callbacks{ActiveProvider: "relay"})
	view := m.View()
	plain := stripANSIInternal(view)
	if !strings.Contains(plain, "Login") {
		t.Errorf("View() for logged-out must show Login; got:\n%s", plain)
	}
}

func TestView_MainMenuShowsHarnessProviderMatrix(t *testing.T) {
	m := newModel(AuthState{LoggedIn: true, Identity: "u@x.com"}, Callbacks{
		ActiveHarness:       "codex",
		ActiveProvider:      "prov:chatgpt-sub",
		ActiveProviderLabel: "ChatGPT relay",
		GrantedProviders: []ProviderRowInfo{
			{ID: "chatgpt-sub", Name: "ChatGPT"},
		},
		ClaudeInstalled: true,
		CodexInstalled:  true,
		PiInstalled:     true,
	})
	plain := stripANSIInternal(m.View())

	for _, want := range []string{
		"◆  Harness",
		"○  Claude Code",
		"◉  OpenAI Codex",
		"○  Pi",
		"◆  Providers",
		"○  DeepSeek relay",
		"◉  ChatGPT relay",
		"◆  What now?",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("main menu View() missing %q without submenu navigation:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "enter select · esc back") {
		t.Fatalf("main menu View() rendered a submenu hint:\n%s", plain)
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
