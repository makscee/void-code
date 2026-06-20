package main

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// updateChoice represents the user's response to the update prompt.
type updateChoice int

const (
	updateChoiceYes    updateChoice = iota + 1 // install now
	updateChoiceNo                             // skip this launch
	updateChoiceAlways                         // always-update
)

// promptUpdate displays the [y]es/[n]o/[a]lways prompt and returns the choice.
// Falls back to updateChoiceNo in non-TTY environments.
//
// In non-interactive mode it never opens the prompt: it prints a one-line
// notice that an update is available and how to apply it, then returns
// updateChoiceNo (do not auto-update, do not block).
func promptUpdate(current, latest string) updateChoice {
	if nonInteractive() {
		fmt.Printf("\n  update available · %s (you have %s) — run `vc update` to install\n", latest, current)
		return updateChoiceNo
	}

	fmt.Printf("\n  update available · %s (you have %s)\n", latest, current)

	m := updatePromptModel{}
	p := tea.NewProgram(m)
	result, err := p.Run()
	if err != nil {
		// Non-TTY: skip silently.
		return updateChoiceNo
	}
	final, ok := result.(updatePromptModel)
	if !ok {
		return updateChoiceNo
	}
	return final.choice
}

// updatePromptModel is a minimal bubbletea model for the [y/n/a] prompt.
type updatePromptModel struct {
	choice updateChoice
	done   bool
}

func (m updatePromptModel) Init() tea.Cmd { return nil }

func (m updatePromptModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "y", "Y":
			m.choice = updateChoiceYes
		case "a", "A":
			m.choice = updateChoiceAlways
		default:
			// Any other key (including 'n', 'N', Esc, Enter) = no.
			m.choice = updateChoiceNo
		}
		m.done = true
		return m, tea.Quit
	}
	return m, nil
}

func (m updatePromptModel) View() string {
	if m.done {
		return ""
	}
	return "  install now? [y]es / [n]o / [a]lways\n"
}

