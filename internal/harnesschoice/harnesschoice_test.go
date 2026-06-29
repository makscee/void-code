package harnesschoice

import "testing"

func TestParseDefaultsToClaude(t *testing.T) {
	for _, s := range []string{"", "claude", "garbage"} {
		if got := Parse(s); got.Kind != Claude {
			t.Fatalf("Parse(%q) = %+v, want Claude", s, got)
		}
	}
}

func TestParsePi(t *testing.T) {
	if got := Parse("pi"); got.Kind != Pi {
		t.Fatalf("Parse(pi) = %+v, want Pi", got)
	}
}

func TestStringAndLabel(t *testing.T) {
	cases := []struct {
		choice Choice
		str    string
		label  string
	}{
		{Choice{Kind: Claude}, "claude", "Claude Code"},
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

	if got := Load(); got.Kind != Claude {
		t.Fatalf("default Load() = %+v, want Claude", got)
	}
	want := Choice{Kind: Pi}
	if err := Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got := Load(); got != want {
		t.Fatalf("Load after Save = %+v, want %+v", got, want)
	}
}
