package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/pibin"
)

// TestRunSpawnGivesPiTheBundledNodeAndNotTheUsersPath is about the class of
// failure that reached a person on 06.09: `vc` answered the first prompt with
//
//	TypeError: zlib.createZstdDecompress is not a function
//
// from inside undici. Pi starts through `#!/usr/bin/env node`, so the node that
// runs it is whichever one PATH names first. On that machine that was an
// nvm-managed v22.12.0, which has no zlib.createZstdDecompress; the v22.23.1
// shipped in ~/.void-code/runtime has it — both binaries were run to check.
// Nothing about VC was broken, and nothing about VC was consulted: the child
// simply inherited os.Environ() whole (buildPiSpawnEnv in main.go) and with it
// the user's PATH.
//
// The desktop closed this class already — desktopChildEnv builds PATH as
// dirname(privateNode):/usr/bin:/bin rather than passing the parent's through
// (desktop/src/main/desktop-child-env.ts) — and the CLI is the half that was
// left. So the requirement here is the desktop's, applied to `vc`: Pi is
// launched with a PATH that was built, its first entry the directory of the
// bundled node, and no directory of the user's on it at all.
//
// It drives runSpawn rather than buildPiSpawnEnv on purpose. A test on the
// builder alone passes while the caller still hands it nothing to build from,
// and that caller is the whole of the defect: the environment reaching Pi is
// what the person's session runs with. The end-to-end shape, the sandboxed
// home, and the fixture-as-Pi trick are borrowed wholesale from
// TestRunSpawnNeverExecutesPathPiWithCredentials in managed_runtime_test.go,
// which guards the neighbouring claim about which Pi runs.
//
// Windows behaviour is stated here as far as running on Windows can state it;
// the platform-by-platform shape of the PATH string, which no run on one
// machine can check, is pinned by internal/childenv instead.
func TestRunSpawnGivesPiTheBundledNodeAndNotTheUsersPath(t *testing.T) {
	if testing.Short() {
		t.Skip("executes shell fixtures")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// Resolve() canonicalizes the home directory before it looks, and on macOS
	// t.TempDir() lives under /var, a symlink to /private/var. Expectations are
	// built on the canonical home for the same reason managed_runtime_test.go
	// builds them there.
	canonicalHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}

	// The user's PATH, in the shape that caused the incident: a version manager's
	// bin directory ahead of everything. It is planted with a `node` in it so the
	// directory is not merely named but genuinely the one a lookup would win from.
	foreignNodeDir := filepath.Join(home, "nvm", "versions", "node", "v22.12.0", "bin")
	if err := os.MkdirAll(foreignNodeDir, 0700); err != nil {
		t.Fatal(err)
	}
	writeExecutableFixture(t, filepath.Join(foreignNodeDir, nodeLookupName()), "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", foreignNodeDir)

	// The bundled runtime, at the layout VC installs: internal/pibin resolves the
	// managed Pi entrypoint from a fixed relative path under ~/.void-code/runtime,
	// and node sits beside it — node/bin/node on unix, node\node.exe on Windows,
	// which is what desktop/scripts/assemble-resources.mjs and
	// assemble-windows-resources.mjs write into the runtime manifest as node.path.
	privateNode := privateNodeFixturePath(home)
	if err := os.MkdirAll(filepath.Dir(privateNode), 0700); err != nil {
		t.Fatal(err)
	}
	writeExecutableFixture(t, privateNode, "#!/bin/sh\nexit 0\n")

	recordedPath := filepath.Join(home, "recorded-path")
	managedPi := managedPiFixturePath(home)
	if err := os.MkdirAll(filepath.Dir(managedPi), 0700); err != nil {
		t.Fatal(err)
	}
	writeExecutableFixture(t, managedPi, pathRecorderScript(t, recordedPath))
	assertManagedPiFixtureIsWhatResolverLooksFor(t, home)

	caPath := filepath.Join(home, "relay-ca.pem")
	if err := os.WriteFile(caPath, []byte("test CA"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VC_RELAY_CA", caPath)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"userId":"u1","email":"u@example.test"}`))
	}))
	defer server.Close()
	t.Setenv("VC_AUTH_HOST", server.URL)
	t.Setenv("VC_ACCESS_CHECK_HOST", server.URL)
	if err := auth.Save("admitted-token"); err != nil {
		t.Fatal(err)
	}

	if err := runSpawn(nil, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(recordedPath)
	if err != nil {
		t.Fatalf("Pi did not run, so nothing was recorded: %v", err)
	}
	got := strings.TrimRight(string(data), "\r\n")

	nodeDir := filepath.Dir(privateNodeFixturePath(canonicalHome))
	want := nodeDir + string(os.PathListSeparator) + systemMinimumPath()
	entries := strings.Split(got, string(os.PathListSeparator))
	// The first entry is compared as a directory rather than as a string: an
	// implementation is free to resolve the runtime through the canonical home,
	// as internal/pibin does, or through HOME as it was given, and on macOS those
	// two spell the same directory differently (/private/var against /var). What
	// is being claimed is which node Pi finds, and that survives either spelling.
	if len(entries) == 0 || !sameDirectory(entries[0], nodeDir) {
		t.Fatalf("Pi was launched with a PATH it inherited, not one built around the bundled node\n got:  %s\n want: %s", got, want)
	}
	if rest := strings.Join(entries[1:], string(os.PathListSeparator)); rest != systemMinimumPath() {
		t.Fatalf("Pi's PATH carries more than the bundled node and the system minimum\n got:  %s\n want: %s", got, want)
	}
	for _, entry := range entries {
		if sameDirectory(entry, foreignNodeDir) {
			t.Fatalf("the user's own node directory reached Pi: %s", got)
		}
	}
}

func TestRunSpawnExecutesBundledNodeWithFixedPiModule(t *testing.T) {
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
	wantModule, err := filepath.EvalSymlinks(piModule)
	if err != nil {
		t.Fatal(err)
	}
	extensionDir := filepath.Join(home, "pi-agent")
	t.Setenv("PI_CODING_AGENT_DIR", extensionDir)

	var executable string
	var args []string
	savedSpawn := spawnHarness
	spawnHarness = func(_ context.Context, gotExecutable string, gotArgs []string, _ []string) error {
		executable = gotExecutable
		args = append([]string(nil), gotArgs...)
		return nil
	}
	t.Cleanup(func() { spawnHarness = savedSpawn })

	if err := runSpawn(nil, nil); err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(executable) || executable != wantNode {
		t.Errorf("spawn executable = %q, want validated bundled Node %q", executable, wantNode)
	}
	wantArgs := []string{wantModule, "-e", filepath.Join(extensionDir, "extensions", "void-code.ts")}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Errorf("spawn args = %#v, want fixed Pi module followed by existing extension args %#v", args, wantArgs)
	}
}

func TestRunSpawnRejectsCorruptBundledNodeWithoutRestoringForeignPath(t *testing.T) {
	for _, kind := range []string{"symlink", "directory", "not executable", "owner mode 0001"} {
		t.Run(kind, func(t *testing.T) {
			if runtime.GOOS == "windows" && kind != "directory" {
				t.Skip("Windows symlink privileges and executable mode do not provide this fixture")
			}
			home, foreignPath := preparePiPathLaunch(t)
			nodePath := privateNodeFixturePath(home)
			if err := os.MkdirAll(filepath.Dir(nodePath), 0700); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "symlink":
				if err := os.Symlink(filepath.Join(foreignPath, nodeLookupName()), nodePath); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(nodePath, 0700); err != nil {
					t.Fatal(err)
				}
			case "not executable":
				if err := os.WriteFile(nodePath, []byte("not executable"), 0600); err != nil {
					t.Fatal(err)
				}
			case "owner mode 0001":
				// This process creates and therefore owns the fixture. An execute bit
				// for "other" must not make it executable by that owner.
				if err := os.WriteFile(nodePath, []byte("not executable by owner"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(nodePath, 0001); err != nil {
					t.Fatal(err)
				}
			}

			_, nodeErr := pibin.ResolveNode()
			if nodeErr == nil {
				t.Fatalf("fixture %q was accepted as a bundled Node", kind)
			}

			spawned := false
			spawnPath := ""
			savedSpawn := spawnHarness
			spawnHarness = func(_ context.Context, _ string, _ []string, env []string) error {
				spawned = true
				spawnPath = pathFromEnv(env)
				return nil
			}
			t.Cleanup(func() { spawnHarness = savedSpawn })

			err := runSpawn(nil, nil)
			if spawned {
				t.Fatalf("managed Pi spawn was reached for rejected bundled Node %q; child PATH %q silently restores foreign PATH %q", kind, spawnPath, foreignPath)
			}
			if err == nil {
				t.Fatalf("runSpawn succeeded for rejected bundled Node %q", kind)
			}
			if !strings.Contains(err.Error(), nodeErr.Error()) {
				t.Fatalf("runSpawn error %q does not report bundled Node rejection %q", err, nodeErr)
			}
		})
	}
}

func TestRunSpawnRejectsBundledNodeTreeWithMissingExecutable(t *testing.T) {
	home, foreignPath := preparePiPathLaunch(t)
	if err := os.MkdirAll(filepath.Join(home, ".void-code", "runtime", "node"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := pibin.ResolveNode(); !os.IsNotExist(err) {
		t.Fatalf("fixture must have a bundled Node tree but no expected executable, got %v", err)
	}

	spawned := false
	spawnPath := ""
	savedSpawn := spawnHarness
	spawnHarness = func(_ context.Context, _ string, _ []string, env []string) error {
		spawned = true
		spawnPath = pathFromEnv(env)
		return nil
	}
	t.Cleanup(func() { spawnHarness = savedSpawn })

	err := runSpawn(nil, nil)
	if spawned {
		t.Fatalf("token-bearing Pi spawn was reached with a partial bundled Node tree; child PATH %q inherited foreign PATH %q", spawnPath, foreignPath)
	}
	if err == nil {
		t.Fatal("runSpawn succeeded with a bundled Node tree missing its expected executable")
	}
}

func TestRunSpawnRejectsMissingBundledNodeTreeWhenRuntimeManifestExists(t *testing.T) {
	home, foreignPath := preparePiPathLaunch(t)
	manifestPath := filepath.Join(home, ".void-code", "runtime", "manifest.json")
	if err := os.WriteFile(manifestPath, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".void-code", "runtime", "node")); !os.IsNotExist(err) {
		t.Fatalf("fixture must have no bundled Node tree, got %v", err)
	}

	spawned := false
	spawnPath := ""
	savedSpawn := spawnHarness
	spawnHarness = func(_ context.Context, _ string, _ []string, env []string) error {
		spawned = true
		spawnPath = pathFromEnv(env)
		return nil
	}
	t.Cleanup(func() { spawnHarness = savedSpawn })

	err := runSpawn(nil, nil)
	if spawned {
		t.Fatalf("token-bearing Pi spawn was reached for a damaged bundled runtime; child PATH %q inherited foreign PATH %q", spawnPath, foreignPath)
	}
	if err == nil {
		t.Fatal("runSpawn succeeded with a runtime manifest but no bundled Node tree")
	}
}

func TestRunSpawnPreservesInheritedPathWhenBundledNodeIsAbsent(t *testing.T) {
	_, foreignPath := preparePiPathLaunch(t)
	if _, err := pibin.ResolveNode(); !os.IsNotExist(err) {
		t.Fatalf("fixture must represent a genuinely absent bundled Node, got %v", err)
	}

	spawned := false
	spawnPath := ""
	savedSpawn := spawnHarness
	spawnHarness = func(_ context.Context, _ string, _ []string, env []string) error {
		spawned = true
		spawnPath = pathFromEnv(env)
		return nil
	}
	t.Cleanup(func() { spawnHarness = savedSpawn })

	if err := runSpawn(nil, nil); err != nil {
		t.Fatalf("legacy install.sh launch failed without a bundled Node: %v", err)
	}
	if !spawned {
		t.Fatal("managed Pi was not spawned for a legacy install without bundled Node")
	}
	if spawnPath != foreignPath {
		t.Fatalf("PATH for a genuinely absent bundled Node = %q, want inherited compatibility PATH %q", spawnPath, foreignPath)
	}
}

func preparePiPathLaunch(t *testing.T) (home, foreignPath string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	foreignPath = filepath.Join(home, "foreign-node-bin")
	if err := os.MkdirAll(foreignPath, 0700); err != nil {
		t.Fatal(err)
	}
	writeExecutableFixture(t, filepath.Join(foreignPath, nodeLookupName()), "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", foreignPath)

	managedPi := managedPiFixturePath(home)
	if err := os.MkdirAll(filepath.Dir(managedPi), 0700); err != nil {
		t.Fatal(err)
	}
	writeExecutableFixture(t, managedPi, "#!/bin/sh\nexit 0\n")
	assertManagedPiFixtureIsWhatResolverLooksFor(t, home)

	caPath := filepath.Join(home, "relay-ca.pem")
	if err := os.WriteFile(caPath, []byte("test CA"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VC_RELAY_CA", caPath)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"userId":"u1","email":"u@example.test"}`))
	}))
	t.Cleanup(server.Close)
	t.Setenv("VC_AUTH_HOST", server.URL)
	t.Setenv("VC_ACCESS_CHECK_HOST", server.URL)
	if err := auth.Save("admitted-token"); err != nil {
		t.Fatal(err)
	}
	return home, foreignPath
}

func pathFromEnv(env []string) string {
	for _, entry := range env {
		name, value, _ := strings.Cut(entry, "=")
		if strings.EqualFold(name, "PATH") {
			return value
		}
	}
	return ""
}

// sameDirectory compares two paths as directories, following symlinks where the
// directory exists. A path that cannot be resolved is compared as written, which
// is what the caller wants for the entries VC composed rather than read.
func sameDirectory(left, right string) bool {
	if left == right {
		return true
	}
	resolvedLeft, leftErr := filepath.EvalSymlinks(left)
	resolvedRight, rightErr := filepath.EvalSymlinks(right)
	return leftErr == nil && rightErr == nil && resolvedLeft == resolvedRight
}

// privateNodeFixturePath is where VC's bundled node lives inside the runtime it
// installs, and therefore where the fixture has to be planted. npm's Windows
// distribution keeps node.exe at the root of the extracted archive; the unix
// tarballs keep it under bin/. Both are named in the runtime manifest the
// desktop assembly scripts write, as node/node.exe and node/bin/node.
func privateNodeFixturePath(home string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(home, ".void-code", "runtime", "node", "node.exe")
	}
	return filepath.Join(home, ".void-code", "runtime", "node", "bin", "node")
}

// nodeLookupName is the file name a PATH lookup for node would actually find on
// the platform the test is running on. Windows resolves only names carrying a
// PATHEXT extension, so an extension-less "node" would be invisible there and
// the hostile half of the fixture would be decoration.
func nodeLookupName() string {
	if runtime.GOOS == "windows" {
		return "node.exe"
	}
	return "node"
}

// systemMinimumPath is what follows the bundled node on Pi's PATH: the desktop's
// rule, written out for the platform this run is on. desktopChildEnv uses
// /usr/bin:/bin on Darwin and %SystemRoot%\System32 on Windows, and there is no
// reason for the CLI to have a second answer.
func systemMinimumPath() string {
	if runtime.GOOS != "windows" {
		return "/usr/bin:/bin"
	}
	systemRoot := os.Getenv("SystemRoot")
	if strings.TrimSpace(systemRoot) == "" {
		systemRoot = `C:\Windows`
	}
	return filepath.Join(systemRoot, "System32")
}

// pathRecorderScript returns a script that copies the PATH it was launched with
// into sink, in the dialect the platform actually executes: a POSIX shell script
// for the cli.js entrypoint, a batch file for the .cmd shim, which harness.Spawn
// routes through cmd.exe. It mirrors tokenRecorderScript next door, for the same
// reasons stated there.
func pathRecorderScript(t *testing.T, sink string) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		return "#!/bin/sh\nprintf %s \"$PATH\" > " + shellQuote(sink) + "\n"
	}
	if strings.ContainsAny(sink, "\"%") {
		t.Fatalf("sink path %q cannot be quoted for cmd.exe", sink)
	}
	return "@echo off\r\n> \"" + sink + "\" echo %PATH%\r\n"
}
