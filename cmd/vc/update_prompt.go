package main

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// updateChoice represents the user's response to the update prompt.
type updateChoice int

const (
	updateChoiceNone   updateChoice = iota
	updateChoiceYes                 // install now
	updateChoiceNo                  // skip this launch
	updateChoiceAlways              // always-update
)

// promptUpdate displays the [y]es/[n]o/[a]lways prompt and returns the choice.
// Falls back to updateChoiceNo in non-TTY environments.
func promptUpdate(current, latest string) updateChoice {
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

