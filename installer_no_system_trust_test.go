package installercontract

// The installers add nothing to the machine's trust stores.
//
// They used to fetch the relay's private CA (void-relay-local-ca) and make the
// whole OS trust it: the login keychain on macOS, the system anchors on Linux,
// CurrentUser\Root on Windows (opt-in). A root in those stores vouches for any
// site to every program on the machine, not just for the relay to vc. Relay and
// auth now serve publicly trusted certificates, so the CA is not needed at all:
// neither installer downloads it or touches a trust store (void-works#71).
//
// Each run below would fail against the old installers: install.sh called
// `security add-trusted-cert` (or sudo + update-ca-*) and both installers asked
// the host for /vc/relay-ca.pem.

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// trustStoreTools are the programs install.sh used to reach a trust store with.
// runMirrorInstall puts a refusing fake of each on PATH that logs the call.
var trustStoreTools = []string{"security", "sudo", "install", "update-ca-certificates", "update-ca-trust"}

func TestShellInstallerAddsNoSystemTrust(t *testing.T) {
	skipInstallShOnWindows(t)

	if testing.Short() {
		t.Skip("runs the shell installer with command fixtures")
	}

	// The host does not even serve the CA: an installer that still depends on
	// it fails here, one that merely stopped trusting it still asks for it.
	existing := "-----BEGIN CERTIFICATE-----\nLEFT-BY-AN-OLD-INSTALL\n-----END CERTIFICATE-----\n"
	r := runMirrorInstall(t, mirrorOpts{primary: "ok", ca: "fail", existingCA: existing})

	if r.code != 0 {
		t.Fatalf("installer exited %d with no relay CA on the host\n%s", r.code, r.combined)
	}
	if calls := mirrorLogLines(r.log, "/vc/relay-ca.pem"); len(calls) != 0 {
		t.Errorf("the installer still fetches the relay CA:\n%s", strings.Join(calls, "\n"))
	}
	for _, tool := range trustStoreTools {
		if calls := mirrorLogLines(r.log, "REFUSED", "/"+tool+" "); len(calls) != 0 {
			t.Errorf("the installer ran %s, a trust-store tool:\n%s", tool, strings.Join(calls, "\n"))
		}
	}
	if strings.Contains(strings.ToLower(r.combined), "relay ca") {
		t.Errorf("the run still talks about the relay CA:\n%s", r.combined)
	}

	// A copy an older install left behind is vc's cache, not the installer's
	// to delete; it is only a file and nothing trusts it by being there.
	if got, err := os.ReadFile(r.caPath); err != nil || string(got) != existing {
		t.Errorf("the installer touched the relay-ca.pem an older install left (err=%v)", err)
	}
}

func TestShellInstallerDryRunPlansNoSystemTrust(t *testing.T) {
	skipInstallShOnWindows(t)

	r := runDryRun(t)
	for _, bad := range []string{"relay-ca", "add-trusted-cert", "update-ca-", "trusted"} {
		if strings.Contains(r, bad) {
			t.Errorf("dry-run still plans %q:\n%s", bad, r)
		}
	}
}

func runDryRun(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	cmd := exec.Command("sh", "install.sh", "--dry-run")
	cmd.Env = append(os.Environ(), "HOME="+home, "VC_AUTH_HOST=http://127.0.0.1:1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, output)
	}
	return string(output)
}

func TestPowerShellInstallerAddsNoSystemTrust(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the PowerShell installer against a local fixture host")
	}

	r := runWindowsInstall(t, winOpts{sums: "ok"})
	if r.code != 0 {
		t.Fatalf("installer exited %d\n%s", r.code, r.combined)
	}
	for _, req := range r.requests {
		if strings.Contains(req, "relay-ca") {
			t.Errorf("the installer still fetches the relay CA: %s", req)
		}
	}
	if strings.Contains(strings.ToLower(r.combined), "relay ca") {
		t.Errorf("the run still talks about the relay CA:\n%s", r.combined)
	}
}

// The runs above cannot see a Windows certificate store (Import-Certificate
// does not exist off Windows) or every Linux branch, so the source is checked
// too: no step that writes a trust store may come back.
func TestInstallersHaveNoTrustStoreStep(t *testing.T) {
	for file, banned := range map[string][]string{
		"install.sh":  {"add-trusted-cert", "update-ca-certificates", "update-ca-trust", "relay-ca.pem"},
		"install.ps1": {"Import-Certificate", `Cert:\`, "certutil", "X509Store", "relay-ca.pem"},
	} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range banned {
			if strings.Contains(string(data), b) {
				t.Errorf("%s still contains %q", file, b)
			}
		}
	}
}
