package main

import (
	"bytes"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/makscee/void-code/internal/config"
	"github.com/makscee/void-code/internal/pibin"
	"github.com/makscee/void-code/internal/runtimechoice"
)

// ─── `vc runtime` ───────────────────────────────────────────────────────────

// execVC runs the real root command with args, as `vc <args>` would.
func execVC(t *testing.T, args ...string) (string, error) {
	t.Helper()
	savedFlag := nonInteractiveFlag
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs(args)
	t.Cleanup(func() {
		nonInteractiveFlag = savedFlag
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	err := rootCmd.Execute()
	return out.String(), err
}

func configBytes(t *testing.T) []byte {
	t.Helper()
	path, err := config.ConfigFilePath()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return data
}

func configKey(t *testing.T, key string) (string, bool) {
	t.Helper()
	kv, err := config.ReadConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	v, ok := kv[key]
	return v, ok
}

// `vc runtime` is configuration, not a launch: it must not sit behind the
// landing screen, which on a non-TTY stdin fails the process before Cobra runs.
func TestRuntimeCommandIsRegisteredAndSkipsTheWelcomeGate(t *testing.T) {
	found := false
	for _, c := range rootCmd.Commands() {
		if c.Name() == "runtime" {
			found = true
		}
	}
	if !found {
		t.Fatal("rootCmd has no `runtime` sub-command")
	}
	if !welcomeGateSkippingSubCommands["runtime"] {
		t.Fatal("`runtime` is missing from welcomeGateSkippingSubCommands")
	}
}

func TestRuntimeCommandSavesAnExplicitChoice(t *testing.T) {
	for _, value := range []string{"codex", "pi"} {
		t.Run(value, func(t *testing.T) {
			withTempHome(t)
			if err := config.WriteConfigFile(map[string]string{"auto_update": "true", "active_harness": "claude"}); err != nil {
				t.Fatal(err)
			}
			menu := stubRuntimeMenu(t, true, runtimechoice.Pi, nil)

			if _, err := execVC(t, "runtime", value); err != nil {
				t.Fatalf("vc runtime %s: %v", value, err)
			}
			if got, _ := configKey(t, "runtime"); got != value {
				t.Fatalf("runtime = %q after `vc runtime %s`", got, value)
			}
			if got, _ := configKey(t, "auto_update"); got != "true" {
				t.Errorf("`vc runtime %s` dropped auto_update (now %q)", value, got)
			}
			if menu.calls != 0 {
				t.Errorf("an explicit value still opened the menu %d times", menu.calls)
			}
		})
	}
}

func TestRuntimeCommandRefusesUnknownValuesAndLeavesConfigAlone(t *testing.T) {
	for _, args := range [][]string{
		{"runtime", "foo"},
		{"runtime", "claude"},
		{"runtime", "pi", "codex"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			withTempHome(t)
			if err := config.WriteConfigFile(map[string]string{"auto_update": "true", "runtime": "pi"}); err != nil {
				t.Fatal(err)
			}
			before := configBytes(t)
			menu := stubRuntimeMenu(t, true, runtimechoice.Codex, nil)

			if _, err := execVC(t, args...); err == nil {
				t.Fatalf("vc %s returned nil", strings.Join(args, " "))
			}
			if after := configBytes(t); !bytes.Equal(before, after) {
				t.Fatalf("refused `vc %s` changed the config:\nbefore: %q\nafter:  %q", strings.Join(args, " "), before, after)
			}
			if menu.calls != 0 {
				t.Errorf("refused value opened the menu %d times", menu.calls)
			}
		})
	}
}

func TestRuntimeCommandWithoutAValueAsksTheMenuAndSavesItsAnswer(t *testing.T) {
	withTempHome(t)
	menu := stubRuntimeMenu(t, true, runtimechoice.Codex, nil)

	if _, err := execVC(t, "runtime"); err != nil {
		t.Fatalf("vc runtime: %v", err)
	}
	if menu.calls != 1 {
		t.Fatalf("menu called %d times, want 1", menu.calls)
	}
	if got, _ := configKey(t, "runtime"); got != "codex" {
		t.Fatalf("runtime = %q after the menu answered codex", got)
	}
}

// Without a terminal there is nobody to answer: the command must not block on
// a prompt, must not guess, and must not write anything.
func TestRuntimeCommandWithoutAValueNeverPromptsWhenNonInteractive(t *testing.T) {
	for name, tc := range map[string]struct {
		attached bool
		args     []string
	}{
		"no terminal":       {false, []string{"runtime"}},
		"--non-interactive": {true, []string{"runtime", "--non-interactive"}},
	} {
		t.Run(name, func(t *testing.T) {
			withTempHome(t)
			if err := config.WriteConfigFile(map[string]string{"auto_update": "true"}); err != nil {
				t.Fatal(err)
			}
			before := configBytes(t)
			menu := stubRuntimeMenu(t, tc.attached, runtimechoice.Codex, nil)

			if _, err := execVC(t, tc.args...); err == nil {
				t.Fatalf("vc %s without a terminal returned nil", strings.Join(tc.args, " "))
			}
			if menu.calls != 0 {
				t.Fatalf("menu opened %d times without an interactive terminal", menu.calls)
			}
			if after := configBytes(t); !bytes.Equal(before, after) {
				t.Fatalf("config changed:\nbefore: %q\nafter:  %q", before, after)
			}
		})
	}
}

// ─── first launch ───────────────────────────────────────────────────────────

type piLaunch struct {
	home     string
	wantNode string
	spawn    *spawnRecord
	ensure   *codexEnsureStub
}

// preparePiRuntimeLaunch plants the bundled Node and Pi module the way
// TestRunSpawnExecutesBundledNodeWithFixedPiModule does, so a Pi launch has
// something to resolve; Codex's installer is stubbed to count calls.
func preparePiRuntimeLaunch(t *testing.T) *piLaunch {
	t.Helper()
	home, _ := preparePiPathLaunch(t)
	privateNode := privateNodeFixturePath(home)
	if err := os.MkdirAll(filepath.Dir(privateNode), 0700); err != nil {
		t.Fatal(err)
	}
	writeExecutableFixture(t, privateNode, "fixture")
	wantNode, err := pibin.ResolveNode()
	if err != nil {
		t.Fatal(err)
	}
	piModule := filepath.Join(home, ".void-code", "runtime", "pi", "node_modules", "@earendil-works", "pi-coding-agent", "dist", "cli.js")
	if err := os.MkdirAll(filepath.Dir(piModule), 0700); err != nil {
		t.Fatal(err)
	}
	writeExecutableFixture(t, piModule, "fixture")
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "pi-agent"))
	noExit(t)
	return &piLaunch{
		home:     home,
		wantNode: wantNode,
		spawn:    recordSpawn(t),
		ensure:   stubEnsureCodex(t, "", errors.New("test: Codex must not be installed on a Pi launch")),
	}
}

func assertPiLaunched(t *testing.T, l *piLaunch) {
	t.Helper()
	if l.spawn.calls != 1 {
		t.Fatalf("spawnHarness called %d times, want 1", l.spawn.calls)
	}
	if l.spawn.exe != l.wantNode {
		t.Fatalf("spawned %q, want the bundled Node %q that runs Pi", l.spawn.exe, l.wantNode)
	}
	if got, n := envCount(l.spawn.env, "VC_HARNESS"); n != 1 || got != "pi" {
		t.Fatalf("VC_HARNESS = %q (present %d times), want exactly one pi", got, n)
	}
	if l.ensure.calls != 0 {
		t.Fatalf("a Pi launch installed Codex (%d calls)", l.ensure.calls)
	}
}

func TestSavedPiRuntimeLaunchesPiWithoutTheMenu(t *testing.T) {
	l := preparePiRuntimeLaunch(t)
	saveRuntimeKey(t, "pi")
	menu := stubRuntimeMenu(t, true, runtimechoice.Codex, nil)

	if err := runSpawn(nil, nil); err != nil {
		t.Fatalf("runSpawn with runtime=pi: %v", err)
	}
	if menu.calls != 0 {
		t.Fatalf("menu opened %d times although runtime=pi is saved", menu.calls)
	}
	assertPiLaunched(t, l)
}

func TestSavedCodexRuntimeLaunchesCodexWithoutTheMenu(t *testing.T) {
	l := prepareCodexLaunch(t, codexGrants, http.StatusOK)
	saveRuntimeKey(t, "codex")
	menu := stubRuntimeMenu(t, true, runtimechoice.Pi, nil)

	if err := runSpawn(nil, nil); err != nil {
		t.Fatalf("runSpawn with runtime=codex: %v", err)
	}
	if menu.calls != 0 {
		t.Fatalf("menu opened %d times although runtime=codex is saved", menu.calls)
	}
	if l.spawn.calls != 1 || l.spawn.exe != l.fakeCodex {
		t.Fatalf("spawned %q (%d calls), want Codex %q once", l.spawn.exe, l.spawn.calls, l.fakeCodex)
	}
}

func TestFirstInteractiveLaunchAsksOnceSavesAndLaunchesCodex(t *testing.T) {
	l := prepareCodexLaunch(t, codexGrants, http.StatusOK)
	menu := stubRuntimeMenu(t, true, runtimechoice.Codex, nil)

	if err := runSpawn(nil, nil); err != nil {
		t.Fatalf("first launch: %v", err)
	}
	if menu.calls != 1 {
		t.Fatalf("menu called %d times on first launch, want 1", menu.calls)
	}
	if got, _ := configKey(t, "runtime"); got != "codex" {
		t.Fatalf("runtime = %q after choosing Codex, want codex", got)
	}
	if l.spawn.calls != 1 || l.spawn.exe != l.fakeCodex {
		t.Fatalf("spawned %q (%d calls), want the chosen Codex %q once", l.spawn.exe, l.spawn.calls, l.fakeCodex)
	}
}

func TestFirstInteractiveLaunchAsksOnceSavesAndLaunchesPi(t *testing.T) {
	l := preparePiRuntimeLaunch(t)
	menu := stubRuntimeMenu(t, true, runtimechoice.Pi, nil)

	if err := runSpawn(nil, nil); err != nil {
		t.Fatalf("first launch: %v", err)
	}
	if menu.calls != 1 {
		t.Fatalf("menu called %d times on first launch, want 1", menu.calls)
	}
	if got, _ := configKey(t, "runtime"); got != "pi" {
		t.Fatalf("runtime = %q after choosing Pi, want pi", got)
	}
	assertPiLaunched(t, l)
}

// A cancelled menu is not a choice: nothing is launched and nothing is saved,
// so the next `vc` asks again.
func TestCancelledFirstLaunchMenuLaunchesNothingAndSavesNothing(t *testing.T) {
	l := prepareCodexLaunch(t, codexGrants, http.StatusOK)
	menu := stubRuntimeMenu(t, true, "", errors.New("menu cancelled"))

	_ = runSpawn(nil, nil)
	if menu.calls != 1 {
		t.Fatalf("menu called %d times, want 1", menu.calls)
	}
	if l.spawn.calls != 0 {
		t.Fatalf("spawned %q after the menu was cancelled", l.spawn.exe)
	}
	if got, ok := configKey(t, "runtime"); ok {
		t.Fatalf("runtime = %q saved after the menu was cancelled", got)
	}
}

func TestNonInteractiveLaunchWithoutAChoiceRunsPiAndSavesNothing(t *testing.T) {
	for name, tc := range map[string]struct {
		attached       bool
		nonInteractive bool
	}{
		"no terminal":       {attached: false},
		"--non-interactive": {attached: true, nonInteractive: true},
	} {
		t.Run(name, func(t *testing.T) {
			l := preparePiRuntimeLaunch(t)
			menu := stubRuntimeMenu(t, tc.attached, runtimechoice.Codex, nil)
			nonInteractiveFlag = tc.nonInteractive

			if err := runSpawn(nil, nil); err != nil {
				t.Fatalf("runSpawn: %v", err)
			}
			if menu.calls != 0 {
				t.Fatalf("menu opened %d times in a non-interactive launch", menu.calls)
			}
			if got, ok := configKey(t, "runtime"); ok {
				t.Fatalf("non-interactive launch saved runtime=%q", got)
			}
			assertPiLaunched(t, l)
		})
	}
}

// active_harness=codex has sat in some configs since June. It is not an answer
// to the runtime question: without a terminal it launches Pi, with one it asks.
func TestLegacyActiveHarnessCodexNeverSelectsCodex(t *testing.T) {
	t.Run("no terminal", func(t *testing.T) {
		l := preparePiRuntimeLaunch(t)
		if err := config.WriteConfigFile(map[string]string{"active_harness": "codex", "active_provider": "chatgpt-new"}); err != nil {
			t.Fatal(err)
		}
		menu := stubRuntimeMenu(t, false, runtimechoice.Codex, nil)

		if err := runSpawn(nil, nil); err != nil {
			t.Fatalf("runSpawn: %v", err)
		}
		if menu.calls != 0 {
			t.Fatalf("menu opened %d times without a terminal", menu.calls)
		}
		if got, ok := configKey(t, "runtime"); ok {
			t.Fatalf("legacy key led to runtime=%q being saved", got)
		}
		assertPiLaunched(t, l)
	})
	t.Run("terminal", func(t *testing.T) {
		l := preparePiRuntimeLaunch(t)
		if err := config.WriteConfigFile(map[string]string{"active_harness": "codex"}); err != nil {
			t.Fatal(err)
		}
		menu := stubRuntimeMenu(t, true, runtimechoice.Pi, nil)

		if err := runSpawn(nil, nil); err != nil {
			t.Fatalf("runSpawn: %v", err)
		}
		if menu.calls != 1 {
			t.Fatalf("menu called %d times; the legacy key must not pre-answer it", menu.calls)
		}
		assertPiLaunched(t, l)
	})
}

// ─── `vc status` ────────────────────────────────────────────────────────────

func runtimeStatusLine(t *testing.T) string {
	t.Helper()
	out, err := captureStdout(t, func() error { return runStatus(nil, nil) })
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "runtime:") {
			return line
		}
	}
	t.Fatalf("vc status printed no runtime line:\n%s", out)
	return ""
}

func TestStatusPrintsTheSavedRuntime(t *testing.T) {
	for _, tc := range []struct {
		config map[string]string
		want   string
		not    string
	}{
		{map[string]string{"runtime": "codex"}, "codex", ""},
		{map[string]string{"runtime": "pi"}, "pi", "codex"},
		{map[string]string{"active_harness": "codex"}, "", "codex"},
	} {
		withTempHome(t) // not logged in: status prints its header and stops, no network
		if err := config.WriteConfigFile(tc.config); err != nil {
			t.Fatal(err)
		}
		line := strings.ToLower(runtimeStatusLine(t))
		if tc.want != "" && !strings.Contains(line, tc.want) {
			t.Errorf("config %v: runtime line %q does not name %s", tc.config, line, tc.want)
		}
		if tc.not != "" && strings.Contains(line, tc.not) {
			t.Errorf("config %v: runtime line %q names %s", tc.config, line, tc.not)
		}
	}
}
