package welcome

import (
	"github.com/makscee/void-code/internal/clackui"
	"github.com/makscee/void-code/internal/harnesschoice"
)

type harnessRow struct {
	label         string
	choice        harnesschoice.Choice
	installResult RunResult
}

type harnessesModel struct {
	rows   []harnessRow
	cursor int
	active string
}

func buildHarnessRows(claudeInstalled, codexInstalled, piInstalled bool) []harnessRow {
	return []harnessRow{
		buildHarnessRow(harnesschoice.Choice{Kind: harnesschoice.Claude}, claudeInstalled, RunInstallClaude),
		buildHarnessRow(harnesschoice.Choice{Kind: harnesschoice.Codex}, codexInstalled, RunInstallCodex),
		buildHarnessRow(harnesschoice.Choice{Kind: harnesschoice.Pi}, piInstalled, RunInstallPi),
	}
}

func buildHarnessRow(choice harnesschoice.Choice, installed bool, installResult RunResult) harnessRow {
	label := choice.Label()
	if !installed {
		label += " (not installed)"
		return harnessRow{label: label, choice: choice, installResult: installResult}
	}
	return harnessRow{label: label, choice: choice}
}

func newHarnessesModel(activeStr string, claudeInstalled, codexInstalled, piInstalled bool) harnessesModel {
	return harnessesModel{rows: buildHarnessRows(claudeInstalled, codexInstalled, piInstalled), active: activeStr}
}

func NewHarnessesModelForTest(activeStr string, piInstalled bool) harnessesModel {
	return newHarnessesModel(activeStr, true, true, piInstalled)
}

func (m harnessesModel) RowCount() int         { return len(m.rows) }
func (m harnessesModel) RowLabel(i int) string { return m.rows[i].label }
func (m harnessesModel) RowNeedsInstall(i int) bool {
	return m.rows[i].installResult != SpawnClaude
}
func (m harnessesModel) RowIsActive(i int) bool {
	return m.rows[i].choice.String() == m.active
}

func (m harnessesModel) render() string {
	var out string
	out += clackui.RailLine("◆", "  "+clackui.InfoTextStyle.Render("Harness")) + "\n"
	for i, r := range m.rows {
		marker := "○"
		if m.RowIsActive(i) {
			marker = "◉"
		}
		style := clackui.UnselectedItemStyle
		if r.installResult != SpawnClaude {
			style = clackui.HintStyle
		}
		if i == m.cursor {
			style = clackui.SelectedItemStyle
			if !m.RowIsActive(i) {
				marker = "●"
			}
		}
		out += clackui.RailLine("│", "  "+style.Render(marker+"  "+r.label)) + "\n"
	}
	out += clackui.RailLine("└", "  "+clackui.HintStyle.Render("↑/↓ · enter select · esc back")) + "\n"
	return out
}
