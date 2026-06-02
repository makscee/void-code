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

func TestRelayProviderRoundTrip(t *testing.T) {
	p := Provider{Kind: RelayProvider, ID: "plat-2"}
	if got := p.String(); got != "prov:plat-2" {
		t.Fatalf("String() = %q, want %q", got, "prov:plat-2")
	}
	if got := Parse("prov:plat-2"); got.Kind != RelayProvider || got.ID != "plat-2" {
		t.Fatalf("Parse() = %+v, want RelayProvider id=plat-2", got)
	}
	if got := p.Label(); got != "plat-2" {
		t.Fatalf("Label() = %q, want %q", got, "plat-2")
	}
}

func TestParseUnknownStillRelay(t *testing.T) {
	// zero-regression: "relay"/""/garbage still decode to the bare Relay kind.
	for _, s := range []string{"relay", "", "garbage", "prov:"} {
		if got := Parse(s); got.Kind != Relay {
			t.Fatalf("Parse(%q).Kind = %v, want Relay", s, got.Kind)
		}
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
