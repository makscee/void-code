package installercontract

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func readInstaller(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

func TestInstallersUsePlainInteractiveLogin(t *testing.T) {
	for _, name := range []string{"install.sh", "install.ps1"} {
		t.Run(name, func(t *testing.T) {
			content := readInstaller(t, name)
			for _, obsolete := range []string{"VC_CODE", "login --code"} {
				if strings.Contains(content, obsolete) {
					t.Errorf("%s contains obsolete %q contract", name, obsolete)
				}
			}
			if !strings.Contains(content, "vc login") {
				t.Errorf("%s does not instruct plain interactive vc login", name)
			}
		})
	}
}

func TestUserDocsDoNotPromiseAutomaticOrAccessCodeLogin(t *testing.T) {
	for _, name := range []string{
		"README.md",
		"docs/mac-setup.md",
		"docs/windows-setup.md",
		"desktop/docs/windows-accountant-pilot-runbook.md",
	} {
		t.Run(name, func(t *testing.T) {
			content := readInstaller(t, name)
			for _, obsolete := range []string{"VC_CODE", "login --code", "login --device", "logs in automatically"} {
				if strings.Contains(content, obsolete) {
					t.Errorf("%s contains obsolete login wording %q", name, obsolete)
				}
			}
		})
	}
}

func TestRootHelpDescribesCurrentLoginFlow(t *testing.T) {
	content := readInstaller(t, "cmd/vc/root.go")
	if strings.Contains(content, "Authenticate with an access code") {
		t.Fatal("root help advertises obsolete access-code login")
	}
	if !strings.Contains(content, "login    Authenticate interactively or with device flow") {
		t.Fatal("root help does not describe the current login flow")
	}
}

func TestStableReleaseSyncsInstallersAndBinariesToVoidAuth(t *testing.T) {
	content := readInstaller(t, ".github/workflows/release.yml")
	for _, required := range []string{
		"out-file-path: void-auth/public/vc/bin",
		"cp install.sh void-auth/public/vc/install.sh",
		"cp install.ps1 void-auth/public/vc/install.ps1",
		"git add public/vc/bin/ public/vc/version.json public/vc/install.sh public/vc/install.ps1",
	} {
		if !strings.Contains(content, required) {
			t.Errorf("stable release workflow is missing %q", required)
		}
	}

	checkout := strings.Index(content, "- name: Checkout void-code")
	sync := strings.Index(content, "cp install.sh void-auth/public/vc/install.sh")
	if checkout < 0 || sync < 0 || checkout >= sync {
		t.Fatal("stable release workflow must check out installer sources before syncing them")
	}
}

func TestShellInstallerProvisionsManagedPiDespiteHealthyPathPi(t *testing.T) {
	skipInstallShOnWindows(t)

	if testing.Short() {
		t.Skip("runs the shell installer with command fixtures")
	}
	home, mockBin := t.TempDir(), t.TempDir()
	writeMock := func(name, source string) {
		t.Helper()
		path := filepath.Join(mockBin, name)
		if err := os.WriteFile(path, []byte(source), 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeMock("pi", "#!/bin/sh\nexit 0\n")
	writeMock("node", "#!/bin/sh\n[ \"$1\" = --version ] && echo v22.0.0\n")
	writeMock("curl", "#!/bin/sh\nout=\nwhile [ $# -gt 0 ]; do\n  if [ \"$1\" = -o ]; then out=$2; shift 2; continue; fi\n  shift\ndone\n[ -z \"$out\" ] || { mkdir -p \"$(dirname \"$out\")\"; printf fixture > \"$out\"; }\n")
	writeMock("npm", "#!/bin/sh\nprefix=\nwhile [ $# -gt 0 ]; do\n  if [ \"$1\" = --prefix ]; then prefix=$2; shift 2; continue; fi\n  shift\ndone\nmkdir -p \"$prefix/node_modules/@earendil-works/pi-coding-agent/dist\"\nprintf '#!/bin/sh\\nexit 0\\n' > \"$prefix/node_modules/@earendil-works/pi-coding-agent/dist/cli.js\"\nchmod 700 \"$prefix/node_modules/@earendil-works/pi-coding-agent/dist/cli.js\"\nprintf invoked > \"$HOME/npm-was-called\"\n")

	cmd := exec.Command("sh", "install.sh")
	cmd.Env = append(os.Environ(), "HOME="+home, "PATH="+mockBin+":/usr/bin:/bin", "VC_SKIP_DOWNLOAD=1", "VC_INSTALL_YES=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("installer failed: %v\n%s", err, output)
	}
	entry := filepath.Join(home, ".void-code", "runtime", "pi", "node_modules", "@earendil-works", "pi-coding-agent", "dist", "cli.js")
	if _, err := os.Stat(entry); err != nil {
		t.Fatalf("healthy PATH pi incorrectly skipped managed provisioning: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(home, "npm-was-called")); err != nil {
		t.Fatalf("installer did not call managed npm install despite healthy PATH pi: %v", err)
	}
}

func TestPowerShellPiContractMatchesWindowsResolverArtifact(t *testing.T) {
	content := readInstaller(t, "install.ps1")
	for _, required := range []string{
		"$piEntry = Join-Path $piRuntimeDir 'node_modules\\.bin\\pi.cmd'",
		"Test-Path -LiteralPath $piEntry -PathType Leaf",
		"& $NpmCommand --prefix $piRuntimeDir install --no-save",
	} {
		if !strings.Contains(content, required) {
			t.Errorf("PowerShell installer is missing Windows Pi artifact contract %q", required)
		}
	}
}

func TestShellInstallerDryRunDoesNotWrite(t *testing.T) {
	skipInstallShOnWindows(t)

	home := t.TempDir()
	before, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("sh", "install.sh", "--dry-run")
	cmd.Env = append(os.Environ(), "HOME="+home, "VC_AUTH_HOST=http://127.0.0.1:1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "NEXT: vc login") {
		t.Fatalf("dry-run output does not instruct plain login:\n%s", output)
	}

	after, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("dry-run wrote under HOME: before=%v after=%v", entryNames(before), entryNames(after))
	}
}

func TestPowerShellDryRunExitsBeforeWrites(t *testing.T) {
	content := readInstaller(t, "install.ps1")
	dryRun := strings.Index(content, "if ($env:VC_INSTALL_DRY_RUN -eq '1')")
	write := strings.Index(content, "New-Item -ItemType Directory")
	if dryRun < 0 || write < 0 || dryRun >= write {
		t.Fatalf("PowerShell dry-run guard must precede filesystem writes")
	}
	guard := content[dryRun:write]
	if !strings.Contains(guard, "NEXT: vc login") || !strings.Contains(guard, "exit 0") {
		t.Fatalf("PowerShell dry-run guard must print plain login guidance and exit before writes")
	}

	powerShell, err := exec.LookPath("pwsh")
	if err != nil {
		powerShell, err = exec.LookPath("powershell")
	}
	if err != nil {
		t.Skip("PowerShell is not available")
	}

	home := t.TempDir()
	cmd := exec.Command(powerShell, "-NoProfile", "-File", "install.ps1")
	cmd.Env = append(os.Environ(), "VC_INSTALL_DRY_RUN=1", "USERPROFILE="+home)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("PowerShell dry-run failed: %v\n%s", err, output)
	}

	// The assertion here used to be "the directory is empty", and that is wrong
	// on Windows — not because the installer misbehaves, but because the shell
	// does. PowerShell 7.5+ writes its own startup cache under $env:USERPROFILE
	// on every launch, so a bare empty-directory check measures pwsh, not
	// install.ps1.
	//
	// It was then narrowed to "none of the paths install.ps1 builds from
	// $env:USERPROFILE appeared", with the list read out of install.ps1 by a
	// `Join-Path $env:USERPROFILE '...'` regexp. That narrowing went too far: it
	// only ever watches doors the extractor recognises. A write in any other
	// spelling — "$env:USERPROFILE\thing" in double quotes, string
	// concatenation, $env:APPDATA (install.ps1:395 and :569 already reach the
	// profile that way) — is neither in the list nor in the guard, and since the
	// list stays non-empty on account of the old `.void-code` entry, the
	// НЕ СМОГ guard below does not fire either. Measured: a probe write of
	// `New-Item -Force -ItemType Directory -Path "$env:USERPROFILE\vc-probe"`
	// placed inside the dry-run guard created the directory and the list-only
	// assertion stayed green.
	//
	// So the broad claim is back, and the exception is subtracted instead of the
	// claim being shrunk: everything that appears under the profile fails, minus
	// the exact entries a bare pwsh launch writes for itself (see
	// pwshStartupCacheEntries). That direction is preferred over merely pinning
	// "install.ps1 builds profile paths in exactly one way", because it catches a
	// write in a spelling nobody predicted rather than only the spellings someone
	// thought to forbid.
	//
	// The list-derived check is kept after it: it names the expected path in the
	// failure message, and its НЕ СМОГ still reports the day install.ps1 stops
	// naming any profile path at all.
	if left := profileEntriesLeftBehind(t, home); len(left) > 0 {
		t.Errorf("PowerShell dry-run wrote under %%USERPROFILE%%: %v\n(nothing but the pwsh startup cache may appear here; the dry-run guard returns before the first write)\n%s",
			left, output)
	}

	for _, rel := range userProfilePathsWrittenByPowerShellInstaller(t, content) {
		path := filepath.Join(home, filepath.FromSlash(rel))
		if info, statErr := os.Stat(path); statErr == nil {
			t.Errorf("PowerShell dry-run created %%USERPROFILE%%\\%s (directory=%v) though it must exit before any write:\n%s",
				rel, info.IsDir(), output)
		} else if !os.IsNotExist(statErr) {
			t.Fatalf("stat %s: %v", path, statErr)
		}
	}
}

// powerShellUserProfilePathRe matches the one way install.ps1 builds a path
// under the user's profile:
//
//	$vcDir = Join-Path $env:USERPROFILE '.void-code'
var powerShellUserProfilePathRe = regexp.MustCompile(`Join-Path \$env:USERPROFILE '([^']+)'`)

// userProfilePathsWrittenByPowerShellInstaller returns the paths, relative to
// %USERPROFILE%, that install.ps1 names for itself — asked of the installer
// rather than remembered here.
//
// It fails the test when it finds none: an assertion built from an empty list
// asserts nothing, and that is exactly how a narrowed check turns into a
// decoration without anybody noticing.
func userProfilePathsWrittenByPowerShellInstaller(t *testing.T, installer string) []string {
	t.Helper()
	seen := map[string]bool{}
	var found []string
	for _, m := range powerShellUserProfilePathRe.FindAllStringSubmatch(installer, -1) {
		rel := strings.ReplaceAll(m[1], `\`, "/")
		if rel == "" || seen[rel] {
			continue
		}
		seen[rel] = true
		found = append(found, rel)
	}
	if len(found) == 0 {
		t.Fatal("НЕ СМОГ: в install.ps1 не нашлось ни одного пути под $env:USERPROFILE — проверять сухой прогон стало не по чему")
	}
	return found
}

// pwshStartupCacheEntries is what a bare pwsh launch writes under
// %USERPROFILE% on its own, before install.ps1 is even parsed. Measured on
// WIN11-VCLAB (Windows 11 Pro), portable pwsh builds, a fresh directory per
// case, USERPROFILE pointed at it:
//
//	pwsh 7.6.5  -NoProfile -Command "exit 0"           → AppData\Local\Microsoft\PowerShell\StartupProfileData-NonInteractive
//	pwsh 7.5.10 -NoProfile -Command "exit 0"           → the same file
//	pwsh 7.6.5  -NoProfile -File install.ps1 (dry run) → the same file, and nothing else
//	pwsh 7.4.6  either way                             → nothing at all
//
// The no-op case never loads install.ps1 and never sets VC_INSTALL_DRY_RUN, yet
// produces the identical tree — so the [AppData] that failed this test on
// windows-latest (run 33512615187, image windows-2025, pwsh 7 from
// C:\Program Files\PowerShell\7) is the shell's, and the installer wrote
// nothing.
//
// Subtracted by exact path, deliberately: the four directories exist only to
// hold that one file. A sibling next to it, another name under
// AppData\Local\Microsoft, or anything at all elsewhere in the profile is not
// excused and fails the test. Widening this to a prefix like "AppData/**" would
// reopen the same hole from the other side.
var pwshStartupCacheEntries = map[string]bool{
	"appdata":                            true,
	"appdata/local":                      true,
	"appdata/local/microsoft":            true,
	"appdata/local/microsoft/powershell": true,
	"appdata/local/microsoft/powershell/startupprofiledata-noninteractive": true,
}

// profileEntriesLeftBehind returns every path under root that is not part of
// the pwsh startup cache, relative to root and slash-separated. Comparison is
// case-insensitive because %USERPROFILE% lives on a case-insensitive
// filesystem and the cache's casing is the shell's business, not ours.
func profileEntriesLeftBehind(t *testing.T, root string) []string {
	t.Helper()
	var left []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if pwshStartupCacheEntries[strings.ToLower(rel)] {
			return nil
		}
		left = append(left, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("НЕ СМОГ: обойти %%USERPROFILE%% (%s) не удалось: %v", root, err)
	}
	return left
}

func entryNames(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, filepath.Base(entry.Name()))
	}
	return names
}
