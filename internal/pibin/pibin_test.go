package pibin

import (
	"go/ast"
	"go/parser"
	"go/token"
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

// messageForRoot is the message as it would be produced on a machine whose install
// root is root. Both variables are set because os.UserHomeDir reads HOME on Unix and
// USERPROFILE on Windows.
func messageForRoot(t *testing.T, root string) string {
	t.Helper()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	return MissingMessage()
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
	bare := messageForRoot(t, root)

	// The same machine, now carrying a managed installation whose Pi runtime is gone.
	// Only the directory is created, so an implementation may look for the install by
	// ~/.void-code or by ~/.void-code/runtime and either way sees an installation here
	// and none above. Which of the two it looks at is its own decision.
	if err := os.MkdirAll(filepath.Join(root, ".void-code", "runtime"), 0700); err != nil {
		t.Fatal(err)
	}
	managed := messageForRoot(t, root)

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

	// The broken-install case keeps the old meaning — reinstall — but has to say which
	// installation is broken, and the only way to know that is to have looked at the
	// install root.
	if !strings.Contains(managed, ".void-code") {
		t.Fatalf("broken-install message does not name the managed runtime location: %q", managed)
	}
}

// TestMissingMessageTakesTheInstallRootAsAnArgument keeps the decision reachable
// without rewriting the environment: the install root has to arrive as a parameter,
// the way desktop-child-env.ts takes the platform as one instead of reading
// process.platform. MissingMessage() stays for its callers as the thin wrapper that
// reads the home directory and passes it in.
func TestMissingMessageTakesTheInstallRootAsAnArgument(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "pibin.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "MissingMessage") {
			continue
		}
		for _, param := range fn.Type.Params.List {
			if ident, ok := param.Type.(*ast.Ident); ok && ident.Name == "string" {
				return
			}
		}
	}
	t.Fatal("no MissingMessage* function in pibin.go takes the install root as a string argument: " +
		"the message is built from whatever os.UserHomeDir returns inside the function, so neither " +
		"situation can be produced deliberately. Add e.g. MissingMessageFor(installRoot string) string " +
		"and leave MissingMessage() as its wrapper.")
}
