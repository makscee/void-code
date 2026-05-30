package provider

import "testing"

func TestParse_RoundTrip(t *testing.T) {
	cases := []struct {
		s    string
		want Provider
	}{
		{"relay", Provider{Kind: Relay}},
		{"plain", Provider{Kind: Plain}},
		{"key:work", Provider{Kind: NamedKey, Name: "work"}},
		{"key:my key", Provider{Kind: NamedKey, Name: "my key"}},
		{"", Provider{Kind: Relay}},        // empty → default relay
		{"garbage", Provider{Kind: Relay}}, // unknown → default relay
	}
	for _, c := range cases {
		got := Parse(c.s)
		if got != c.want {
			t.Errorf("Parse(%q) = %+v, want %+v", c.s, got, c.want)
		}
	}
}

func TestString_RoundTrip(t *testing.T) {
	for _, p := range []Provider{
		{Kind: Relay},
		{Kind: Plain},
		{Kind: NamedKey, Name: "work"},
	} {
		if got := Parse(p.String()); got != p {
			t.Errorf("round-trip %+v → %q → %+v", p, p.String(), got)
		}
	}
}

func TestLabel(t *testing.T) {
	if l := (Provider{Kind: Relay}).Label(); l != "Relay (void-relay)" {
		t.Errorf("Relay label = %q", l)
	}
	if l := (Provider{Kind: Plain}).Label(); l != "Plain Claude Code" {
		t.Errorf("Plain label = %q", l)
	}
	if l := (Provider{Kind: NamedKey, Name: "work"}).Label(); l != "key: work" {
		t.Errorf("NamedKey label = %q", l)
	}
}

func TestLoadSave_PersistsViaConfig(t *testing.T) {
	// Redirect HOME so config writes to a temp dir.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp) // Windows

	// Default (nothing saved) → Relay.
	if got := Load(); got.Kind != Relay {
		t.Fatalf("default Load = %+v, want Relay", got)
	}
	want := Provider{Kind: NamedKey, Name: "work"}
	if err := Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got := Load(); got != want {
		t.Fatalf("Load after Save = %+v, want %+v", got, want)
	}
}
