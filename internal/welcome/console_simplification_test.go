package welcome_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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

// The Top up screen sends the person to the pay page, not to Maks.
func TestTopUpScreenShowsThePayLink(t *testing.T) {
	m := welcome.NewMenuModelForTest(welcome.AuthState{LoggedIn: true, Identity: "member@example.test"})
	for i := 0; i < m.ItemCount(); i++ {
		if m.ItemLabel(i) == "Пополнить" {
			m = m.SetCursor(i)
		}
	}
	if m.ItemLabel(m.Cursor()) != "Пополнить" {
		t.Fatalf("no «Пополнить» menu item")
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	view := next.View()
	if !strings.Contains(view, "https://profile.makscee.ru/vc/pay") {
		t.Errorf("Top up screen has no pay link:\n%s", view)
	}
	if strings.Contains(view, "@makscee") {
		t.Errorf("Top up screen still sends people to @makscee:\n%s", view)
	}
}
