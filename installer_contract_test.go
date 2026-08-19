package installercontract

import (
	"os"
	"os/exec"
	"path/filepath"
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
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("PowerShell dry-run wrote under USERPROFILE: %v", entryNames(entries))
	}
}

func TestWindowsInstallerPathRepairContract(t *testing.T) {
	content := readInstaller(t, "install.ps1")
	for _, required := range []string{
		"# BEGIN VC PATH HELPERS",
		"function Merge-VCPathEntry",
		"[StringComparison]::OrdinalIgnoreCase",
		"function Join-VCProcessPath",
		"function Send-VCEnvironmentChange",
		"SendMessageTimeout",
		"WM_SETTINGCHANGE",
		"Get-Command vc",
		"& $target --version",
		"restored the previous vc.exe",
		"Fully exit all VS Code windows and Code.exe processes, then reopen VS Code.",
		`& "$env:USERPROFILE\.void-code\bin\vc.exe" status`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("Windows installer PATH repair is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		`$userPath -notlike "*$binDir*"`,
		`$env:PATH -notlike "*$binDir*"`,
		"Stop-Process",
		"taskkill",
	} {
		if strings.Contains(content, forbidden) {
			t.Errorf("Windows installer contains forbidden stale/unsafe behavior %q", forbidden)
		}
	}
}

func TestStableReleaseRequiresPowerShell51InstallerGate(t *testing.T) {
	content := readInstaller(t, ".github/workflows/release.yml")
	for _, required := range []string{
		"windows-installer-powershell51:",
		"runs-on: windows-2022",
		"expected Windows PowerShell 5.1",
		"needs: [build, windows-installer-powershell51]",
	} {
		if !strings.Contains(content, required) {
			t.Errorf("stable release is missing PowerShell 5.1 gate %q", required)
		}
	}
}

func TestWindowsSetupDocumentsNoReinstallFallback(t *testing.T) {
	content := readInstaller(t, "docs/windows-setup.md")
	for _, required := range []string{
		"fully exit all VS Code windows and `Code.exe` processes",
		`& "$env:USERPROFILE\.void-code\bin\vc.exe" status`,
		"Do not reinstall or log in again.",
	} {
		if !strings.Contains(content, required) {
			t.Errorf("Windows setup guidance is missing %q", required)
		}
	}
	if strings.Contains(content, "run the installer again") {
		t.Fatal("Windows PATH troubleshooting still recommends reinstalling")
	}
}

func TestPowerShellPathHarness(t *testing.T) {
	powerShell, err := exec.LookPath("pwsh")
	if err != nil {
		powerShell, err = exec.LookPath("powershell")
	}
	if err != nil {
		t.Skip("PowerShell is not available")
	}

	cmd := exec.Command(powerShell, "-NoProfile", "-File", "scripts/test-install-ps1-path.ps1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("PowerShell PATH harness failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "PASS: 20 installer PATH assertions") {
		t.Fatalf("unexpected PATH harness output:\n%s", output)
	}
}

func entryNames(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, filepath.Base(entry.Name()))
	}
	return names
}
