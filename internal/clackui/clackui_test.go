package clackui_test

import (
	"strings"
	"testing"

	"github.com/makscee/void-code/internal/clackui"
)

func TestRailLine_ContainsPrefix(t *testing.T) {
	glyphs := []string{"┌", "│", "└", "◇", "◆"}
	for _, g := range glyphs {
		out := clackui.RailLine(g, "  content")
		if !strings.Contains(out, g) {
			t.Errorf("RailLine(%q) missing glyph in output %q", g, out)
		}
		if !strings.Contains(out, "content") {
			t.Errorf("RailLine(%q) missing content in output %q", g, out)
		}
	}
}

func TestRailLine_EmptyContent(t *testing.T) {
	out := clackui.RailLine("│", "")
	if !strings.Contains(out, "│") {
		t.Errorf("RailLine with empty content missing │: %q", out)
	}
}

func TestStyles_RenderDoNotDropContent(t *testing.T) {
	// Smoke-test that all exported style vars render content through.
	cases := []struct {
		name  string
		style interface{ Render(...string) string }
	}{
		{"RailStyle", clackui.RailStyle},
		{"TitleStyle", clackui.TitleStyle},
		{"InfoTextStyle", clackui.InfoTextStyle},
		{"SelectedItemStyle", clackui.SelectedItemStyle},
		{"UnselectedItemStyle", clackui.UnselectedItemStyle},
		{"HintStyle", clackui.HintStyle},
		{"WarnStyle", clackui.WarnStyle},
		{"OkStyle", clackui.OkStyle},
		{"FailStyle", clackui.FailStyle},
	}
	for _, c := range cases {
		out := c.style.Render("test")
		if !strings.Contains(out, "test") {
			t.Errorf("Style %s Render lost content: %q", c.name, out)
		}
	}
}
