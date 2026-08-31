package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// This file holds the half of the single-owner spec that can be stated in terms
// of behaviour vc already exposes: the guarantees one writer keeps and the other
// breaks (criteria 2 and 3), the coupling between the seeded pair and the
// extension that has to accept it (criterion 8), and the pair the seed must not
// invent (criterion 9). None of it names updatePiSettings, so this file compiles
// against today's tree and fails for the reason the spec describes rather than
// for a missing symbol. The lock lives in pi_settings_owner_test.go.

// The managed web-search package path is opaque to settings.json — any string
// will do, the reconciler only ever compares it to what it finds in "packages".
const contractPackagePath = "/managed/pi-web-access-0.13.0-void.1"

// piSettingsWriters is the whole set of code paths in vc that rewrite
// settings.json. Criteria 2 and 3 are about a guarantee holding no matter which
// of them ran, so every one of them is driven through the same assertions; a
// third writer added later belongs in this table and nowhere else.
var piSettingsWriters = []struct {
	name  string
	write func(t *testing.T)
}{
	{
		name: "default-model seed",
		write: func(t *testing.T) {
			t.Helper()
			if err := ensurePiDefaultModel(); err != nil {
				t.Fatalf("ensurePiDefaultModel() error = %v", err)
			}
		},
	},
	{
		name: "web-search package reconciler",
		write: func(t *testing.T) {
			t.Helper()
			if err := reconcileManagedPackageSetting(contractPackagePath, true); err != nil {
				t.Fatalf("reconcileManagedPackageSetting() error = %v", err)
			}
		},
	},
}

// Acceptance criterion 2: an integer past float64 precision survives a write by
// EITHER writer. 9007199254740993 is 2^53+1 — the smallest integer a float64
// cannot hold; a decode through float64 and back writes 9007199254740992, one
// less, silently. The assertion is on the digits on disk, not on a decoded
// value, because a test that decodes the same wrong way cannot see the damage.
func TestPiSettingsWritersPreserveIntegersBeyondFloat64(t *testing.T) {
	const untouchable = "9007199254740993"
	for _, writer := range piSettingsWriters {
		t.Run(writer.name, func(t *testing.T) {
			dir := piSettingsSandbox(t)
			path := writePiSettings(t, dir, `{"lastSessionSeq": `+untouchable+`, "theme": "nord"}`, 0600)

			writer.write(t)

			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(after), untouchable) {
				t.Errorf("lastSessionSeq rewritten through float64: %s\nwant the digits %s to survive verbatim", after, untouchable)
			}
		})
	}
}

// Acceptance criterion 3: the mode the user gave the file survives a write by
// EITHER writer. 0644 is the case that matters — a user who widened the file
// must not find it narrowed back, and a writer that hardcodes 0600 does exactly
// that without saying so.
//
// The claim is "the mode did not change", stated against the mode actually
// observed before the write rather than against the literal 0644, because those
// are the same number only on POSIX. Windows has no permission bits for Go to
// report: os.Stat gives 0666 for any ordinary file and 0444 for a read-only
// one, and os.Chmod moves only the read-only flag. Asserting 0644 there demands
// something the platform cannot represent, and the test fails on correct code.
//
// Said plainly, because the difference matters when reading a green run: on
// Windows this subtest is a weaker claim. It catches a writer that marks the
// file read-only, and it cannot catch the defect this criterion was written for
// — a hardcoded 0600 also reports 0666, so the two are indistinguishable. The
// real content of criterion 3 is verified on POSIX; on Windows the test is
// present, honest, and narrower. It is not skipped: a writer that flipped the
// read-only flag would be a genuine bug on the platform where users actually
// hit read-only files.
func TestPiSettingsWritersPreserveExistingFileMode(t *testing.T) {
	for _, writer := range piSettingsWriters {
		t.Run(writer.name, func(t *testing.T) {
			dir := piSettingsSandbox(t)
			path := writePiSettings(t, dir, `{"theme":"nord"}`, 0644)

			before := statPerm(t, path)
			// On POSIX the fixture must really carry 0644, or the assertion
			// below compares the write against a mode nobody asked for.
			if runtime.GOOS != "windows" && before != 0644 {
				t.Fatalf("fixture mode = %04o, want 0644 — the test cannot say anything about a mode it failed to set", before)
			}

			writer.write(t)

			if got := statPerm(t, path); got != before {
				t.Errorf("settings.json mode = %04o, want %04o unchanged (this write is an update, not a re-creation)", got, before)
			}
		})
	}
}

func statPerm(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

// Acceptance criterion 8: the pair vc seeds is only useful if the extension that
// receives it registers that provider and accepts that model. The two live in
// different languages — Go constants here, a JS source string in
// pi_extension.go — so nothing but this test connects them. Every assertion is
// built FROM the Go constants, so changing either side alone breaks it.
//
// Breaking it on purpose is the check that it means anything: flip
// piDefaultModel to "gpt-5.6-luna" and the CODEX_MODEL_ID assertion must fail.
func TestPiDefaultPairIsWiredIntoTheExtensionThatMustAcceptIt(t *testing.T) {
	t.Run("extension declares the same pair", func(t *testing.T) {
		for _, want := range []string{
			`const CODEX_PROVIDER_ID = "` + piDefaultProvider + `";`,
			`const CODEX_MODEL_ID = "` + piDefaultModel + `";`,
			`pi.registerProvider(CODEX_PROVIDER_ID,`,
		} {
			if !strings.Contains(piVoidCodexExtensionSource, want) {
				t.Errorf("Pi extension source missing %q — the seeded pair and the extension have diverged", want)
			}
		}
	})

	// The seed writes a model the extension will be asked to serve. If that
	// model is not in the codex provider's allowed set, the extension filters
	// it out and Pi opens on a model that does not exist — with every test
	// green. Asserting on the identifier rather than the literal is deliberate:
	// combined with the CODEX_MODEL_ID assertion above it pins the value, and
	// it keeps working if the set is ever reordered.
	t.Run("extension allows the seeded model", func(t *testing.T) {
		const marker = "const allowed = new Set([CODEX_MODEL_ID"
		if !strings.Contains(piVoidCodexExtensionSource, marker) {
			t.Fatalf("Pi extension source missing %q — the codex allowed-set no longer admits the seeded model", marker)
		}
	})

	// Third copy of the same string, and the one resolvePiManagedModel checks a
	// user's choice against: a model absent from here is refused as unsupported.
	t.Run("relay model list carries the seeded model", func(t *testing.T) {
		found := false
		for _, model := range piVoidCodexModels {
			if model == piDefaultModel {
				found = true
			}
		}
		if !found {
			t.Errorf("piVoidCodexModels = %q, want it to contain the seeded %q", piVoidCodexModels, piDefaultModel)
		}
	})
}

// Acceptance criterion 9: the seed must not assemble a pair no provider can
// serve. A user who picked void-deepseek and no model gets gpt-5.6-terra
// appended today — a model the deepseek branch of the extension filters out,
// so Pi opens on a provider/model pair that does not exist. Seeding only the
// model was right when there was one provider; with two it invents a
// combination the user never chose.
func TestEnsurePiDefaultModelDoesNotInventAProviderModelPair(t *testing.T) {
	t.Run("foreign provider chosen: nothing is seeded", func(t *testing.T) {
		dir := piSettingsSandbox(t)
		const body = `{"defaultProvider":"void-deepseek","theme":"nord"}`
		path := writePiSettings(t, dir, body, 0600)

		if err := ensurePiDefaultModel(); err != nil {
			t.Fatalf("ensurePiDefaultModel() error = %v", err)
		}

		got := readPiSettings(t, path)
		if _, ok := got["defaultModel"]; ok {
			t.Errorf("defaultModel = %#v was seeded next to defaultProvider %q, which cannot serve it", got["defaultModel"], "void-deepseek")
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != body {
			t.Errorf("file rewritten although there was nothing to seed\n got: %s\nwant: %s", after, body)
		}
	})

	// The other side of the rule — the provider the seed owns still gets its
	// model — is TestEnsurePiDefaultModelAddsOnlyModelWhenItsOwnProviderIsSet
	// in pi_settings_test.go, which asserts exactly that and is not repeated
	// here. What stays is the fresh install the seed exists for: no provider
	// chosen at all, so vc picks both halves and they match by construction.
	// Between the two, criterion 9 cannot be satisfied by seeding nothing ever.
	t.Run("nothing chosen: both halves are seeded", func(t *testing.T) {
		dir := piSettingsSandbox(t)

		if err := ensurePiDefaultModel(); err != nil {
			t.Fatalf("ensurePiDefaultModel() error = %v", err)
		}

		assertDefaultsWritten(t, readPiSettings(t, filepath.Join(dir, "settings.json")))
	})
}
