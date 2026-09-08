package pibin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedPiPathUsesPlatformPackageEntrypoint(t *testing.T) {
	home := filepath.Join("test home", "user")
	for _, tc := range []struct {
		goos string
		want string
	}{
		{"linux", filepath.Join(home, ".void-code", "runtime", "pi", "node_modules", "@earendil-works", "pi-coding-agent", "dist", "cli.js")},
		{"darwin", filepath.Join(home, ".void-code", "runtime", "pi", "node_modules", "@earendil-works", "pi-coding-agent", "dist", "cli.js")},
		{"windows", filepath.Join(home, ".void-code", "runtime", "pi", "node_modules", ".bin", "pi.cmd")},
	} {
		t.Run(tc.goos, func(t *testing.T) {
			if got := managedPiPathForOS(home, tc.goos); got != tc.want {
				t.Fatalf("managedPiPathForOS() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveUsesManagedRuntimeNotPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	malicious := filepath.Join(home, "malicious")
	if err := os.MkdirAll(malicious, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(malicious, "pi"), []byte("malicious"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", malicious)

	// Resolve canonicalizes the home directory with filepath.EvalSymlinks before
	// building the managed path, so the expectation must be canonical too. On
	// macOS t.TempDir() lives under /var/folders and /var is a symlink to
	// /private/var: comparing against the uncanonicalized path fails there while
	// passing in Linux CI, which is why this went unnoticed.
	canonicalHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	path := managedPiPath(canonicalHome)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("managed"), 0700); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got != path {
		t.Fatalf("Resolve() = %q, want managed path %q", got, path)
	}
}

func TestResolveRejectsSymlinkedRuntimeParent(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("symlink permissions vary on Windows")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "runtime", "pi", "node_modules", "@earendil-works", "pi-coding-agent", "dist"), 0700); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(outside, "runtime", "pi", "node_modules", "@earendil-works", "pi-coding-agent", "dist", "cli.js")
	if err := os.WriteFile(entry, []byte("managed"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, ".void-code")); err != nil {
		t.Fatal(err)
	}

	if _, err := Resolve(); err == nil {
		t.Fatal("Resolve accepted a managed runtime below a symlinked parent")
	}
}

// downloadPage is the one concrete step a person holding a bare binary can act on.
// 27.08 an external tester downloaded vc-darwin-arm64 from the release page next to
// the desktop bundles, ran it, and was told to re-run an installer she had never run.
// The bare assets cannot leave the release — install.sh falls back to them as its
// mirror — so the address has to be in the message instead.
const downloadPage = "https://auth.makscee.ru/download"

// legacyMissingMessage is the text that failed on that tester. It is here to be
// forbidden, not to be matched.
const legacyMissingMessage = "VC managed Pi runtime not found — Pi must be provisioned by VC\n" +
	"Re-run the VC installer to provision its managed Pi runtime."

// legacyInstallInstruction is the sentence she could not act on: there was no
// installer to re-run.
const legacyInstallInstruction = "Re-run the VC installer to provision its managed Pi runtime."

// installManagedRuntime puts a managed installation into root whose Pi runtime is
// gone: ~/.void-code/runtime exists, the entrypoint under it does not. Only the
// directory is created, so an implementation may recognise the installation by
// ~/.void-code or by ~/.void-code/runtime; which of the two it looks at is its own
// decision.
func installManagedRuntime(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".void-code", "runtime"), 0700); err != nil {
		t.Fatal(err)
	}
}

// TestMissingMessageSeparatesBareBinaryFromBrokenInstall pins the difference, not the
// wording: one text for a binary that was never installed by VC, another for an
// installation whose runtime is broken. A single text cannot be right for both, and
// that is what sent the tester nowhere.
func TestMissingMessageSeparatesBareBinaryFromBrokenInstall(t *testing.T) {
	// Both situations are produced under the same root, so any difference between the
	// two texts comes from the situation and not from the path they were produced
	// under: a message that merely echoes its own directory would otherwise look
	// different for free.
	root := t.TempDir()

	// No managed installation at all — someone who unpacked a release binary into
	// ~/Downloads and ran it from there.
	bare := MissingMessageFor(root)

	// The same machine, now carrying an installation whose Pi runtime is gone.
	installManagedRuntime(t, root)
	managed := MissingMessageFor(root)

	if strings.TrimSpace(bare) == strings.TrimSpace(managed) {
		t.Fatalf("a binary that was never installed and an installation with a broken runtime get the same text: %q", bare)
	}

	for _, tc := range []struct {
		situation string
		message   string
	}{
		{"bare binary", bare},
		{"broken managed install", managed},
	} {
		if strings.TrimSpace(tc.message) == "" {
			t.Fatalf("%s: message is empty", tc.situation)
		}
		if tc.message == legacyMissingMessage {
			t.Fatalf("%s: still the text that failed on a live tester: %q", tc.situation, tc.message)
		}
		// Named for the same reason claudebin's message names claude: the missing
		// thing has to appear in the sentence about it.
		if !strings.Contains(tc.message, "Pi") {
			t.Fatalf("%s: message never names Pi, so it does not say what is missing: %q", tc.situation, tc.message)
		}
	}

	if !strings.Contains(bare, downloadPage) {
		t.Fatalf("bare-binary message does not name %s, so there is nowhere to go from it: %q", downloadPage, bare)
	}
	if strings.Contains(bare, legacyInstallInstruction) {
		t.Fatalf("bare-binary message still sends a person to re-run an installer that was never run: %q", bare)
	}

	// The difference has to be structural, not decorative — this is the thing the
	// reader takes away from the message. Nothing is installed, so the bare-binary
	// text has no installation to point at and must point at none; the broken-install
	// text names the one it found, which is how the reader learns VC is installed here
	// and where to look. Two texts that both name the path differ in wording only, and
	// a person reading either one still cannot tell which situation they are in.
	if strings.Contains(bare, root) {
		t.Fatalf("bare-binary message points at an installation under %q where none exists: %q", root, bare)
	}
	if !strings.Contains(managed, filepath.Join(root, ".void-code")) {
		t.Fatalf("broken-install message does not name the installation it found under %q: %q", root, managed)
	}
}

// TestMissingMessageForAnswersAboutItsArgument makes the argument load-bearing. A
// function that takes installRoot and then asks os.UserHomeDir anyway is
// indistinguishable from an honest one while the two agree, so here they disagree:
// the environment and the argument are put in opposite situations, and the message
// has to be about the argument. The install root travels as a parameter for this
// reason, the way desktop-child-env.ts takes the platform as one instead of reading
// process.platform.
func TestMissingMessageForAnswersAboutItsArgument(t *testing.T) {
	installed := t.TempDir()
	installManagedRuntime(t, installed)
	bare := t.TempDir()

	// HOME and USERPROFILE both, because os.UserHomeDir reads HOME on Unix and
	// USERPROFILE on Windows.
	t.Run("argument bare, environment installed", func(t *testing.T) {
		t.Setenv("HOME", installed)
		t.Setenv("USERPROFILE", installed)
		if got := MissingMessageFor(bare); strings.Contains(got, installed) {
			t.Fatalf("MissingMessageFor(%q) describes the installation under the home directory %q instead: %q", bare, installed, got)
		}
	})

	t.Run("argument installed, environment bare", func(t *testing.T) {
		t.Setenv("HOME", bare)
		t.Setenv("USERPROFILE", bare)
		if got := MissingMessageFor(installed); !strings.Contains(got, installed) {
			t.Fatalf("MissingMessageFor(%q) never names the installation it was handed, so it answered about something else: %q", installed, got)
		}
	})
}

// TestMissingMessageDelegatesForTheCurrentHome covers the entry point every caller in
// cmd/vc actually reaches: it has to be the same decision, taken about the machine it
// runs on, and not a third text drifting beside the two above.
func TestMissingMessageDelegatesForTheCurrentHome(t *testing.T) {
	for _, tc := range []struct {
		situation string
		installed bool
	}{
		{"bare binary", false},
		{"broken managed install", true},
	} {
		t.Run(tc.situation, func(t *testing.T) {
			root := t.TempDir()
			if tc.installed {
				installManagedRuntime(t, root)
			}
			t.Setenv("HOME", root)
			t.Setenv("USERPROFILE", root)

			// On macOS t.TempDir lives under /var, which is a symlink to /private/var.
			// An implementation that canonicalizes the home before building the text is
			// right to do so, so either spelling is accepted — the same correction
			// TestResolveUsesManagedRuntimeNotPath carries above.
			canonical, err := filepath.EvalSymlinks(root)
			if err != nil {
				t.Fatal(err)
			}
			want := MissingMessageFor(root)
			if got := MissingMessage(); got != want && got != MissingMessageFor(canonical) {
				t.Fatalf("MissingMessage() = %q, want the message for its own install root: %q", got, want)
			}
		})
	}
}
