package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func decodePiSettingsSnapshot(t *testing.T, data []byte) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var settings map[string]any
	if err := decoder.Decode(&settings); err != nil {
		t.Fatalf("decode Pi settings snapshot: %v\n%s", err, data)
	}
	return settings
}

// A terminal launch must retire the legacy provider before handing control to Pi.
func TestTerminalSessionMigratesLegacyDeepSeekBeforeLaunchingPi(t *testing.T) {
	home, _ := preparePiPathLaunch(t)
	dir := filepath.Join(home, "pi-agent")
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	t.Setenv("VC_PI_MANAGED_WEB_SEARCH", "0")
	path := writePiSettings(t, dir, `{"defaultProvider":"void-deepseek","defaultModel":"deepseek/deepseek-v4-pro","permissions":{"allow":["read"]}}`, 0600)

	var settingsAtLaunch []byte
	savedSpawn := spawnHarness
	spawnHarness = func(context.Context, string, []string, []string) error {
		var err error
		settingsAtLaunch, err = os.ReadFile(path)
		return err
	}
	t.Cleanup(func() { spawnHarness = savedSpawn })

	if err := runSpawn(nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(settingsAtLaunch) == 0 {
		t.Fatal("terminal handed control to Pi without an observable settings file")
	}
	assertDefaultsWritten(t, decodePiSettingsSnapshot(t, settingsAtLaunch))
}

// The desktop's separate startup path must complete the same migration before its Pi process starts.
func TestDesktopSessionMigratesLegacyDeepSeekBeforeLaunchingPi(t *testing.T) {
	dir := piSettingsSandbox(t)
	path := writePiSettings(t, dir, `{"defaultProvider":"void-deepseek","defaultModel":"deepseek/deepseek-v4-flash","permissions":{"allow":["read"]}}`, 0600)
	probe := &desktopSeedProbe{settingsPath: path}

	warnings, err := execDesktopSession(t, probe)
	if err != nil {
		t.Fatalf("desktop-session returned %v; warnings=%q", err, warnings)
	}
	if !probe.ran || !probe.settingsAtLaunchOK {
		t.Fatal("desktop did not reach Pi with an observable settings file")
	}
	assertDefaultsWritten(t, decodePiSettingsSnapshot(t, probe.settingsAtLaunch))
}
