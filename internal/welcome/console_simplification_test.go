package welcome_test

import (
	"strings"
	"testing"

	"github.com/makscee/void-code/internal/welcome"
)

func TestConsoleOffersOnlySubscriptionActionsNotProviderOrHarnessControls(t *testing.T) {
	m := welcome.NewMenuModelForTest(welcome.AuthState{LoggedIn: true, Identity: "member@example.test"})
	view := m.View()
	for _, forbidden := range []string{"Change provider", "Change harness", "Providers", "Harness", "Claude Code", "OpenAI Codex", "DeepSeek relay", "ChatGPT relay"} {
		if strings.Contains(view, forbidden) {
			t.Errorf("console still exposes obsolete choice %q:\n%s", forbidden, view)
		}
	}
	if !strings.Contains(view, "Start") {
		t.Fatal("authenticated subscription must offer Start")
	}
}
