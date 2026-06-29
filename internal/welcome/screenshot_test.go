//go:build evidence

package welcome

import (
	"fmt"
	"strings"
	"testing"
)

// TestEvidence_ProvidersMenuRender renders the Providers menu to stdout for
// screenshot capture. Run with: go test -tags evidence ./internal/welcome/ -v -run TestEvidence_ProvidersMenu
func TestEvidence_ProvidersMenuRender(t *testing.T) {
	m := NewProvidersModelWithGrantedForTest(nil, []ProviderRowInfo{{ID: "deepseek", Name: "DeepSeek"}}, "prov:deepseek")

	fmt.Println("=== Providers Menu TUI (rendered) ===")
	// Strip ANSI for plain capture
	rendered := m.render()
	// Print plain version with markers visible
	fmt.Print(stripANSI(rendered))
	fmt.Println("=== End Providers Menu ===")

	// Assertions on content
	if !strings.Contains(rendered, "Providers") {
		t.Error("missing Providers header")
	}
	if !strings.Contains(rendered, "DeepSeek relay") {
		t.Error("missing DeepSeek relay row")
	}
	if !strings.Contains(rendered, "Plain harness run") {
		t.Error("missing Plain harness run row")
	}
}

// stripANSI removes ANSI escape sequences for plain text capture.
func stripANSI(s string) string {
	var out strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			// skip until 'm' or other terminator
			i += 2
			for i < len(s) && (s[i] < '@' || s[i] > '~') {
				i++
			}
			if i < len(s) {
				i++ // skip terminator
			}
			continue
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}
