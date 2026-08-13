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

func entryNames(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, filepath.Base(entry.Name()))
	}
	return names
}
