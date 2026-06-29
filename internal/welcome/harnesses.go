package welcome

import (
	"github.com/makscee/void-code/internal/clackui"
	"github.com/makscee/void-code/internal/harnesschoice"
)

type harnessRow struct {
	label     string
	choice    harnesschoice.Choice
	installPi bool
}

type harnessesModel struct {
	rows   []harnessRow
	cursor int
	active string
}

func buildHarnessRows(piInstalled bool) []harnessRow {
	rows := []harnessRow{{label: harnesschoice.Choice{Kind: harnesschoice.Claude}.Label(), choice: harnesschoice.Choice{Kind: harnesschoice.Claude}}}
	pi := harnesschoice.Choice{Kind: harnesschoice.Pi}
	label := pi.Label()
	installPi := false
	if !piInstalled {
		label = "Pi (not installed)"
		installPi = true
	}
	rows = append(rows, harnessRow{label: label, choice: pi, installPi: installPi})
	return rows
}

func newHarnessesModel(activeStr string, piInstalled bool) harnessesModel {
	return harnessesModel{rows: buildHarnessRows(piInstalled), active: activeStr}
}

func NewHarnessesModelForTest(activeStr string, piInstalled bool) harnessesModel {
	return newHarnessesModel(activeStr, piInstalled)
}

func (m harnessesModel) RowCount() int         { return len(m.rows) }
func (m harnessesModel) RowLabel(i int) string { return m.rows[i].label }
func (m harnessesModel) RowNeedsInstall(i int) bool {
	return m.rows[i].installPi
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
		if r.installPi {
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
