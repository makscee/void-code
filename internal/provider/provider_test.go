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
	if l := (Provider{Kind: Relay}).Label(); l != "DeepSeek relay" {
		t.Errorf("Relay label = %q", l)
	}
	if l := (Provider{Kind: Plain}).Label(); l != "Plain harness run" {
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

// VCD-78: label persistence and backfill.

func TestSaveLabel_PersistedAndLoadedVerbatim(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	const want = "Relay: DeepSeek"
	if err := SaveLabel(want); err != nil {
		t.Fatalf("SaveLabel: %v", err)
	}
	if got := LoadLabel(); got != want {
		t.Errorf("LoadLabel() = %q, want %q", got, want)
	}
}

func TestLoadLabel_BackfillFromActiveProvider_NamedKey(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	// Persist active_provider without a label (simulates existing user).
	if err := Save(Provider{Kind: NamedKey, Name: "mykey"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// LoadLabel must derive "key: mykey" without crashing.
	got := LoadLabel()
	if got == "" {
		t.Fatal("LoadLabel() returned empty string — must never be empty")
	}
	// Must contain the key name.
	if got != "key: mykey" {
		t.Errorf("LoadLabel() = %q, want %q", got, "key: mykey")
	}
}

func TestLoadLabel_BackfillFromActiveProvider_Relay(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	// No config at all → relay default label.
	got := LoadLabel()
	if got == "" {
		t.Fatal("LoadLabel() returned empty string for default relay case")
	}
	// Relay defaults to "DeepSeek relay".
	wantRelayLabel := (Provider{Kind: Relay}).Label()
	if got != wantRelayLabel {
		t.Errorf("LoadLabel() = %q, want %q", got, wantRelayLabel)
	}
}

func TestLoadLabel_PersistOverridesBackfill(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	// Save provider + a different menu label (e.g. relay provider with friendly name).
	if err := Save(Provider{Kind: RelayProvider, ID: "plat-1"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := SaveLabel("Relay: DeepSeek"); err != nil {
		t.Fatalf("SaveLabel: %v", err)
	}
	// Persisted label wins over derived "plat-1".
	if got := LoadLabel(); got != "Relay: DeepSeek" {
		t.Errorf("LoadLabel() = %q, want %q", got, "Relay: DeepSeek")
	}
}

// VCD-79: ReconcileLabel + FriendlyLabel tests.

func TestReconcileLabel_RelayProvider_PersistsFriendlyLabel(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	// Pre-VCD-78 state: active_provider=prov:plat-2, no label.
	if err := Save(Provider{Kind: RelayProvider, ID: "plat-2"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Verify pre-state: LoadLabel falls back to raw id.
	if got := LoadLabel(); got != "plat-2" {
		t.Errorf("pre-state LoadLabel() = %q, want raw id %q", got, "plat-2")
	}

	// Reconcile with a granted list that contains plat-2.
	granted := []GrantedEntry{{ID: "plat-2", Name: "Claude Sub"}}
	if err := ReconcileLabel(granted); err != nil {
		t.Fatalf("ReconcileLabel: %v", err)
	}

	// After reconcile: LoadLabel returns the friendly label.
	const want = "Relay: Claude Sub"
	if got := LoadLabel(); got != want {
		t.Errorf("post-reconcile LoadLabel() = %q, want %q", got, want)
	}
}

func TestReconcileLabel_FetchFailure_NoClobber(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	// Provider with an existing friendly label.
	if err := Save(Provider{Kind: RelayProvider, ID: "plat-2"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := SaveLabel("Relay: Claude Sub"); err != nil {
		t.Fatalf("SaveLabel: %v", err)
	}

	// Fetch failure: pass empty granted list (provider not found → no clobber).
	if err := ReconcileLabel(nil); err != nil {
		t.Fatalf("ReconcileLabel: %v", err)
	}

	// Label must be unchanged.
	if got := LoadLabel(); got != "Relay: Claude Sub" {
		t.Errorf("after fetch-failure ReconcileLabel, LoadLabel() = %q, want %q", got, "Relay: Claude Sub")
	}
}

func TestReconcileLabel_LabelRefreshOnRename(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	// Provider with old friendly label.
	if err := Save(Provider{Kind: RelayProvider, ID: "plat-2"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := SaveLabel("Relay: Old Name"); err != nil {
		t.Fatalf("SaveLabel: %v", err)
	}

	// Server renames provider.
	granted := []GrantedEntry{{ID: "plat-2", Name: "New Name"}}
	if err := ReconcileLabel(granted); err != nil {
		t.Fatalf("ReconcileLabel: %v", err)
	}

	// Label must reflect the new name.
	const want = "Relay: New Name"
	if got := LoadLabel(); got != want {
		t.Errorf("after rename reconcile, LoadLabel() = %q, want %q", got, want)
	}
}

func TestReconcileLabel_NonRelayProvider_AlwaysRefreshes(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	cases := []struct {
		prov Provider
		want string
	}{
		{Provider{Kind: NamedKey, Name: "mykey"}, "key: mykey"},
		{Provider{Kind: Plain}, "Plain harness run"},
		{Provider{Kind: Relay}, "DeepSeek relay"},
	}
	for _, tc := range cases {
		// Reset config for each case.
		if err := Save(tc.prov); err != nil {
			t.Fatalf("Save %+v: %v", tc.prov, err)
		}
		// No pre-existing label — reconcile with empty granted list.
		if err := ReconcileLabel(nil); err != nil {
			t.Fatalf("ReconcileLabel: %v", err)
		}
		if got := LoadLabel(); got != tc.want {
			t.Errorf("prov=%+v: LoadLabel() = %q, want %q", tc.prov, got, tc.want)
		}
	}
}

func TestFriendlyLabel_RelayProviderNameFallsBackToID(t *testing.T) {
	p := Provider{Kind: RelayProvider, ID: "plat-9"}
	// Entry present but Name is empty — should fall back to id.
	granted := []GrantedEntry{{ID: "plat-9", Name: ""}}
	got := FriendlyLabel(p, granted)
	if got != "Relay: plat-9" {
		t.Errorf("FriendlyLabel() = %q, want %q", got, "Relay: plat-9")
	}
}

func TestFriendlyLabel_RelayProviderNotInList_ReturnsEmpty(t *testing.T) {
	p := Provider{Kind: RelayProvider, ID: "plat-2"}
	got := FriendlyLabel(p, nil)
	if got != "" {
		t.Errorf("FriendlyLabel() for missing provider = %q, want empty", got)
	}
}
