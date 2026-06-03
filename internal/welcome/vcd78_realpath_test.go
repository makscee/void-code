package welcome

import (
	"fmt"
	"strings"
	"testing"
)

func stripForProof(s string) string {
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

// TestVCD78_TitleScreenFriendlyLabel is the real-path proof:
// verifies prov:<id> shows friendly label, never raw id.
func TestVCD78_TitleScreenFriendlyLabel(t *testing.T) {
	cases := []struct {
		activeProvider      string
		activeProviderLabel string
		wantContains        string
		wantAbsent          string
	}{
		// prov:<id> with persisted friendly label — must show label, not raw id
		{
			activeProvider:      "prov:plat-2",
			activeProviderLabel: "Relay: DeepSeek",
			wantContains:        "Relay: DeepSeek",
			wantAbsent:          "plat-2",
		},
		// prov:<id> without persisted label — fallback must not crash
		{
			activeProvider:      "prov:plat-99",
			activeProviderLabel: "",
			wantContains:        "Provider:",
			wantAbsent:          "",
		},
	}
	for _, c := range cases {
		cb := Callbacks{
			ActiveProvider:      c.activeProvider,
			ActiveProviderLabel: c.activeProviderLabel,
		}
		m := newModel(AuthState{LoggedIn: true, Identity: "u@x.com"}, cb)
		view := stripForProof(m.View())
		fmt.Printf("=== activeProvider=%q label=%q ===\n%s\n", c.activeProvider, c.activeProviderLabel, view)
		if !strings.Contains(view, c.wantContains) {
			t.Errorf("want %q in view, not found\nView:\n%s", c.wantContains, view)
		}
		if c.wantAbsent != "" && strings.Contains(view, c.wantAbsent) {
			t.Errorf("raw id %q must NOT appear in view\nView:\n%s", c.wantAbsent, view)
		}
	}
}
