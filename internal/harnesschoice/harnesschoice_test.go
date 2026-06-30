package harnesschoice

import "testing"

func TestParseDefaultsToPi(t *testing.T) {
	for _, s := range []string{"", "pi", "garbage"} {
		if got := Parse(s); got.Kind != Pi {
			t.Fatalf("Parse(%q) = %+v, want Pi", s, got)
		}
	}
}

func TestParseClaudeAndCodex(t *testing.T) {
	if got := Parse("claude"); got.Kind != Claude {
		t.Fatalf("Parse(claude) = %+v, want Claude", got)
	}
	if got := Parse("codex"); got.Kind != Codex {
		t.Fatalf("Parse(codex) = %+v, want Codex", got)
	}
}

func TestStringAndLabel(t *testing.T) {
	cases := []struct {
		choice Choice
		str    string
		label  string
	}{
		{Choice{Kind: Claude}, "claude", "Claude Code"},
		{Choice{Kind: Codex}, "codex", "OpenAI Codex"},
		{Choice{Kind: Pi}, "pi", "Pi"},
	}
	for _, c := range cases {
		if got := c.choice.String(); got != c.str {
			t.Errorf("String() = %q, want %q", got, c.str)
		}
		if got := c.choice.Label(); got != c.label {
			t.Errorf("Label() = %q, want %q", got, c.label)
		}
	}
}

func TestLoadSave(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	if got := Load(); got.Kind != Pi {
		t.Fatalf("default Load() = %+v, want Pi", got)
	}
	want := Choice{Kind: Pi}
	if err := Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got := Load(); got != want {
		t.Fatalf("Load after Save = %+v, want %+v", got, want)
	}
}
