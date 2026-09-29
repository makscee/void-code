package pibin

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// These cover what vc checks before it hands its token to a Node and Pi it
// finds on disk: the happy path, each refusal, and the legacy install.sh state
// that has no bundled Node at all.

// fakeHome points HOME at a fresh directory and returns its canonical path,
// the one the resolvers build on (on macOS t.TempDir is under the /var symlink).
func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	canonical, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func writeFile(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), mode); err != nil {
		t.Fatal(err)
	}
	// WriteFile's mode is filtered by the umask and ignored for existing files.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func skipSymlinksOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}
}

func skipExecBitsOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no execute bits")
	}
}

func modulePath(home string) string {
	return filepath.Join(home, filepath.FromSlash(managedPiRelativePath))
}

// --- ResolveNode ---

func TestResolveNodeReturnsBundledNode(t *testing.T) {
	home := fakeHome(t)
	node := managedNodePath(home)
	writeFile(t, node, 0700)

	got, err := ResolveNode()
	if err != nil {
		t.Fatal(err)
	}
	if got != node {
		t.Fatalf("ResolveNode() = %q, want %q", got, node)
	}
}

func TestResolveNodeLegacyInstallIsUnprovisioned(t *testing.T) {
	for name, setup := range map[string]func(home string){
		"nothing installed": func(string) {},
		"only runtime/pi": func(home string) {
			writeFile(t, managedPiPath(home), 0700)
		},
	} {
		t.Run(name, func(t *testing.T) {
			home := fakeHome(t)
			setup(home)

			_, err := ResolveNode()
			if !errors.Is(err, ErrBundledNodeUnprovisioned) {
				t.Fatalf("ResolveNode() error = %v, want ErrBundledNodeUnprovisioned", err)
			}
			if !os.IsNotExist(err) {
				t.Fatalf("ResolveNode() error = %v, want it to stay os.IsNotExist for older callers", err)
			}
		})
	}
}

// A bundled runtime whose manifest is still there but whose node tree is gone
// was broken after install; falling back to the machine Node would hide that.
func TestResolveNodeManifestWithoutNodeIsCorrupt(t *testing.T) {
	home := fakeHome(t)
	writeFile(t, filepath.Join(home, ".void-code", "runtime", "manifest.json"), 0600)

	_, err := ResolveNode()
	if !errors.Is(err, ErrBundledNodeCorrupt) {
		t.Fatalf("ResolveNode() error = %v, want ErrBundledNodeCorrupt", err)
	}
	if errors.Is(err, ErrBundledNodeUnprovisioned) {
		t.Fatal("a corrupt bundled runtime must not read as a legacy install")
	}
}

// Inside a provisioned node tree, a missing binary is a broken install, not
// the legacy state that allows the PATH fallback.
func TestResolveNodeMissingBinaryInProvisionedTree(t *testing.T) {
	home := fakeHome(t)
	if err := os.MkdirAll(managedNodeRuntimePath(home), 0700); err != nil {
		t.Fatal(err)
	}

	_, err := ResolveNode()
	if err == nil || errors.Is(err, ErrBundledNodeUnprovisioned) {
		t.Fatalf("ResolveNode() error = %v, want a missing-file error that is not ErrBundledNodeUnprovisioned", err)
	}
	if !os.IsNotExist(err) {
		t.Fatalf("ResolveNode() error = %v, want a not-exist error", err)
	}
}

func TestResolveNodeRejectsNonRegularFile(t *testing.T) {
	home := fakeHome(t)
	if err := os.MkdirAll(managedNodePath(home), 0700); err != nil {
		t.Fatal(err)
	}

	_, err := ResolveNode()
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("ResolveNode() error = %v, want a not-a-regular-file refusal", err)
	}
}

func TestResolveNodeRejectsNonExecutable(t *testing.T) {
	skipExecBitsOnWindows(t)
	if os.Geteuid() == 0 {
		t.Skip("root may execute any file")
	}
	home := fakeHome(t)
	writeFile(t, managedNodePath(home), 0600)

	_, err := ResolveNode()
	if err == nil || !strings.Contains(err.Error(), "not executable") {
		t.Fatalf("ResolveNode() error = %v, want a not-executable refusal", err)
	}
}

func TestResolveNodeRejectsSymlinks(t *testing.T) {
	skipSymlinksOnWindows(t)
	for _, tc := range []struct {
		name string
		link func(home, outside string) error
		// A symlink above runtime/node must not look like a legacy install,
		// even when its target is gone.
		dangling bool
	}{
		{"node binary", func(home, outside string) error {
			writeFile(t, filepath.Join(outside, "node"), 0700)
			if err := os.MkdirAll(filepath.Dir(managedNodePath(home)), 0700); err != nil {
				return err
			}
			return os.Symlink(filepath.Join(outside, "node"), managedNodePath(home))
		}, false},
		{"runtime/node", func(home, outside string) error {
			writeFile(t, filepath.Join(outside, "bin", "node"), 0700)
			if err := os.MkdirAll(filepath.Join(home, ".void-code", "runtime"), 0700); err != nil {
				return err
			}
			return os.Symlink(outside, managedNodeRuntimePath(home))
		}, false},
		{".void-code", func(home, outside string) error {
			writeFile(t, filepath.Join(outside, "runtime", "node", "bin", "node"), 0700)
			return os.Symlink(outside, filepath.Join(home, ".void-code"))
		}, false},
		{"dangling .void-code", func(home, outside string) error {
			return os.Symlink(filepath.Join(outside, "gone"), filepath.Join(home, ".void-code"))
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := fakeHome(t)
			if err := tc.link(home, t.TempDir()); err != nil {
				t.Fatal(err)
			}

			_, err := ResolveNode()
			if err == nil || !strings.Contains(err.Error(), "symlink") {
				t.Fatalf("ResolveNode() error = %v, want a symlink refusal", err)
			}
			if errors.Is(err, ErrBundledNodeUnprovisioned) {
				t.Fatal("a redirected runtime must not read as a legacy install")
			}
		})
	}
}

// --- ResolveModule ---

func TestResolveModuleReturnsCliJS(t *testing.T) {
	home := fakeHome(t)
	// A JavaScript module run as `node cli.js` need not be executable.
	writeFile(t, modulePath(home), 0600)

	got, err := ResolveModule()
	if err != nil {
		t.Fatal(err)
	}
	if got != modulePath(home) {
		t.Fatalf("ResolveModule() = %q, want %q", got, modulePath(home))
	}
}

func TestResolveModuleFailures(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		fakeHome(t)
		if _, err := ResolveModule(); !os.IsNotExist(err) {
			t.Fatalf("ResolveModule() error = %v, want not-exist", err)
		}
	})
	t.Run("directory", func(t *testing.T) {
		home := fakeHome(t)
		if err := os.MkdirAll(modulePath(home), 0700); err != nil {
			t.Fatal(err)
		}
		_, err := ResolveModule()
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("ResolveModule() error = %v, want a not-a-regular-file refusal", err)
		}
	})
	t.Run("symlinked cli.js", func(t *testing.T) {
		skipSymlinksOnWindows(t)
		home := fakeHome(t)
		outside := filepath.Join(t.TempDir(), "cli.js")
		writeFile(t, outside, 0600)
		if err := os.MkdirAll(filepath.Dir(modulePath(home)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, modulePath(home)); err != nil {
			t.Fatal(err)
		}
		_, err := ResolveModule()
		if err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("ResolveModule() error = %v, want a symlink refusal", err)
		}
	})
}

// --- Resolve / IsInstalled ---

func TestResolveFailures(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		fakeHome(t)
		if _, err := Resolve(); !os.IsNotExist(err) {
			t.Fatalf("Resolve() error = %v, want not-exist", err)
		}
		if IsInstalled() {
			t.Fatal("IsInstalled() = true with no managed Pi")
		}
	})
	t.Run("directory", func(t *testing.T) {
		home := fakeHome(t)
		if err := os.MkdirAll(managedPiPath(home), 0700); err != nil {
			t.Fatal(err)
		}
		_, err := Resolve()
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("Resolve() error = %v, want a not-a-regular-file refusal", err)
		}
	})
	t.Run("not executable", func(t *testing.T) {
		skipExecBitsOnWindows(t)
		home := fakeHome(t)
		writeFile(t, managedPiPath(home), 0600)
		_, err := Resolve()
		if err == nil || !strings.Contains(err.Error(), "not executable") {
			t.Fatalf("Resolve() error = %v, want a not-executable refusal", err)
		}
		if IsInstalled() {
			t.Fatal("IsInstalled() = true for a non-executable entrypoint")
		}
	})
}

func TestIsInstalled(t *testing.T) {
	home := fakeHome(t)
	writeFile(t, managedPiPath(home), 0700)
	if !IsInstalled() {
		t.Fatal("IsInstalled() = false with a managed Pi in place")
	}
}

// Resolving under a home that is itself a symlink works: the home is
// canonicalized first, and only components below it are checked.
func TestResolveAcceptsSymlinkedHome(t *testing.T) {
	skipSymlinksOnWindows(t)
	real := fakeHome(t)
	link := filepath.Join(t.TempDir(), "home-link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", link)
	writeFile(t, managedPiPath(real), 0700)

	got, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got != managedPiPath(real) {
		t.Fatalf("Resolve() = %q, want %q", got, managedPiPath(real))
	}
}

func TestResolversFailWithoutHome(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-home")
	t.Setenv("HOME", missing)
	t.Setenv("USERPROFILE", missing)
	for name, resolve := range map[string]func() (string, error){
		"Resolve":       Resolve,
		"ResolveModule": ResolveModule,
		"ResolveNode":   ResolveNode,
	} {
		if _, err := resolve(); err == nil || !strings.Contains(err.Error(), "canonicalize VC home") {
			t.Errorf("%s() error = %v, want a canonicalize-home failure", name, err)
		}
	}
}

// --- rejectSymlinkComponents ---

func TestRejectSymlinkComponentsRefusesPathsOutsideHome(t *testing.T) {
	home := t.TempDir()
	for _, path := range []string{
		filepath.Dir(home),
		filepath.Join(filepath.Dir(home), "elsewhere", "pi"),
	} {
		err := rejectSymlinkComponents(home, path)
		if err == nil || !strings.Contains(err.Error(), "escapes canonical home") {
			t.Errorf("rejectSymlinkComponents(%q) error = %v, want an escape refusal", path, err)
		}
	}
}

func TestSplitPath(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"", nil},
		{".", nil},
		{"a", []string{"a"}},
		{filepath.Join("a", "b", "c"), []string{"a", "b", "c"}},
	} {
		got := splitPath(tc.in)
		if strings.Join(got, "|") != strings.Join(tc.want, "|") || len(got) != len(tc.want) {
			t.Errorf("splitPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// --- MissingMessageFor ---

// A plain file named .void-code is not an installation.
func TestMissingMessageForIgnoresNonDirectoryMarker(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, installMarker), 0600)
	if got := MissingMessageFor(root); strings.Contains(got, root) {
		t.Fatalf("MissingMessageFor treats a file named .void-code as an installation: %q", got)
	}
	if got, want := MissingMessageFor(""), MissingMessageFor(t.TempDir()); got != want {
		t.Fatalf("MissingMessageFor(\"\") = %q, want the bare-binary text %q", got, want)
	}
}

func TestManagedNodePathForOS(t *testing.T) {
	home := filepath.Join("test home", "user")
	for goos, want := range map[string]string{
		"linux":   filepath.Join(home, ".void-code", "runtime", "node", "bin", "node"),
		"darwin":  filepath.Join(home, ".void-code", "runtime", "node", "bin", "node"),
		"windows": filepath.Join(home, ".void-code", "runtime", "node", "node.exe"),
	} {
		if got := managedNodePathForOS(home, goos); got != want {
			t.Errorf("managedNodePathForOS(%q) = %q, want %q", goos, got, want)
		}
	}
}
