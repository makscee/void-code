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
	if !strings.Contains(view, "Запустить") {
		t.Fatal("authenticated subscription must offer «Запустить»")
	}
}

// The screen speaks one language, Russian, like the wallet line and the
// top-up screen next to it (void-board#373: «Start / Run doctor» sat next to
// «Пополнить»).
func TestWelcomeScreenIsRussian(t *testing.T) {
	for _, state := range []welcome.AuthState{
		{LoggedIn: true, Identity: "member@example.test", Balance: "0 ₽ · T1"},
		{LoggedIn: true, IdentityUnverified: true},
		{LoggedIn: true, Identity: "member@example.test", IdentityUnverified: true},
		{},
	} {
		m := welcome.NewMenuModelForTest(state)
		var labels []string
		for i := 0; i < m.ItemCount(); i++ {
			labels = append(labels, m.ItemLabel(i))
		}
		for _, text := range []string{m.View(), welcome.PlainBannerForTest(state)} {
			for _, english := range []string{"Start", "Login", "Run doctor", "Open profile", "What now", "quit", "Not logged in", "identity", "unverified", "Logged in", "Identity", "subscription console"} {
				if strings.Contains(text, english) {
					t.Errorf("welcome screen has the English %q (items %q):\n%s", english, labels, text)
				}
			}
		}
	}
	m := welcome.NewMenuModelForTest(welcome.AuthState{LoggedIn: true, Identity: "member@example.test"})
	var labels []string
	for i := 0; i < m.ItemCount(); i++ {
		labels = append(labels, m.ItemLabel(i))
	}
	if got, want := strings.Join(labels, " / "), "Запустить / Пополнить / Проверить установку / Открыть профиль"; got != want {
		t.Errorf("menu = %q, want %q", got, want)
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
	for _, want := range []string{"Пополнить баланс", "Оплатить здесь: https://profile.makscee.ru/vc/pay", "Нажмите любую клавишу, чтобы вернуться"} {
		if !strings.Contains(view, want) {
			t.Errorf("Top up screen lacks %q:\n%s", want, view)
		}
	}
}
