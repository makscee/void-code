package installercontract

// The installers take the relay CA older installers trusted system-wide out of
// the OS trust store again (void-works#71).
//
// Every run here drives the real install.sh or install.ps1 against fakes of the
// trust-store tools, so each branch runs on any runner: a fake `uname -s` picks
// install.sh's macOS or Linux branch, VC_TEST_ROOT moves the Linux anchors into
// a scratch dir, and PowerShell functions stand in for the Cert: drive, which
// does not exist off Windows.
//
// What the runs pin:
//   - the old CA goes, and only it: the keychain copy by its SHA-1, the anchor
//     files by their fixed names, the CurrentUser\Root entry by its subject;
//   - the installer says what is happening before the prompt that removal can
//     raise (fakes print a marker the moment they would prompt, into the same
//     stream, so the order is checked in the output itself);
//   - a refused prompt fails nothing: the install finishes, exits 0 and prints
//     the command that finishes the job by hand;
//   - a machine without the CA sees no prompt and no word about it
//     (installer_no_system_trust_test.go).

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const oldRelayCASHA1 = "5749964BB2A0E0DC72554BE9726F784D033EDB23"

// A stateful `security`: the old CA is in the keychain while $FAKE_KC_STATE
// exists. delete-certificate prints a prompt marker (where macOS would ask for
// the password) and, unless FAKE_KC_DELETE=refuse, removes the state.
const fakeSecurityScript = `#!/bin/sh
printf 'security %s\n' "$*" >> "$FAKE_LOG"
case "$1" in
  find-certificate)
    [ -f "$FAKE_KC_STATE" ] || exit 44
    printf 'keychain: "%s"\n' "$FAKE_KC"
    printf 'SHA-1 hash: ` + oldRelayCASHA1 + `\n'
    printf '    "labl"<blob>="void-relay-local-ca"\n'
    ;;
  delete-certificate)
    printf '<<PASSWORD PROMPT>>\n' >&2
    [ "$FAKE_KC_DELETE" = refuse ] && exit 1
    rm -f "$FAKE_KC_STATE"
    ;;
  *) exit 1 ;;
esac
`

// sudo prints a prompt marker on first use and then runs the command, unless
// FAKE_SUDO=refuse (a wrong password, or no terminal to ask on).
const fakeSudoScript = `#!/bin/sh
printf 'sudo %s\n' "$*" >> "$FAKE_LOG"
if [ ! -f "$FAKE_LOG.sudo" ]; then : > "$FAKE_LOG.sudo"; printf '<<SUDO PROMPT>>\n' >&2; fi
[ "$FAKE_SUDO" = refuse ] && exit 1
exec "$@"
`

const fakeLoggerScript = `#!/bin/sh
printf '%s %s\n' "$(basename "$0")" "$*" >> "$FAKE_LOG"
exit 0
`

// requireBefore fails unless `first` appears in out, and before `then`.
func requireBefore(t *testing.T, out, first, then string) {
	t.Helper()
	i, j := strings.Index(out, first), strings.Index(out, then)
	if i < 0 {
		t.Fatalf("the run never says %q:\n%s", first, out)
	}
	if j < 0 {
		t.Fatalf("the run never reached %q:\n%s", then, out)
	}
	if i > j {
		t.Errorf("%q came only after %q:\n%s", first, then, out)
	}
}

func TestShellInstallerRemovesOldRelayCAFromKeychain(t *testing.T) {
	skipInstallShOnWindows(t)
	if testing.Short() {
		t.Skip("runs the shell installer with command fixtures")
	}

	run := func(t *testing.T, deleteMode string) (mirrorResult, string, string) {
		state := filepath.Join(t.TempDir(), "kc-has-old-ca")
		if err := os.WriteFile(state, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		var kc string
		r := runMirrorInstall(t, mirrorOpts{
			primary: "ok",
			uname:   "Darwin",
			fakes:   map[string]string{"security": fakeSecurityScript},
			env:     []string{"FAKE_KC_STATE=" + state, "FAKE_KC_DELETE=" + deleteMode},
			prepare: func(home, _ string) {
				kc = filepath.Join(home, "Library", "Keychains", "login.keychain-db")
				if err := os.MkdirAll(filepath.Dir(kc), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(kc, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			},
		})
		if r.code != 0 {
			t.Fatalf("installer exited %d\n%s", r.code, r.combined)
		}
		return r, state, kc
	}

	t.Run("removed, after saying why", func(t *testing.T) {
		r, state, kc := run(t, "ok")

		want := "security delete-certificate -Z " + oldRelayCASHA1 + " -t " + kc
		if calls := mirrorLogLines(r.log, want); len(calls) != 1 {
			t.Errorf("want exactly one %q, got:\n%s", want, strings.Join(r.log, "\n"))
		}
		if _, err := os.Stat(state); !os.IsNotExist(err) {
			t.Errorf("the old CA is still in the keychain after the run")
		}
		requireBefore(t, r.combined, "removing the old void-relay CA from your login keychain", "<<PASSWORD PROMPT>>")
		requireBefore(t, r.combined, "may ask for your password", "<<PASSWORD PROMPT>>")
		requireBefore(t, r.combined, "<<PASSWORD PROMPT>>", "removed the old void-relay CA")
		for _, bad := range []string{"add-trusted-cert", "add-certificates"} {
			if calls := mirrorLogLines(r.log, bad); len(calls) != 0 {
				t.Errorf("the installer added trust: %s", strings.Join(calls, "\n"))
			}
		}
	})

	t.Run("a refused password leaves the install whole and says how to finish", func(t *testing.T) {
		r, state, kc := run(t, "refuse")

		if _, err := os.Stat(state); err != nil {
			t.Fatalf("fixture: the refused delete removed the CA anyway")
		}
		if _, err := os.Stat(r.vcPath); err != nil {
			t.Errorf("vc is not installed after a refused CA removal: %v", err)
		}
		requireBefore(t, r.combined, "<<PASSWORD PROMPT>>", "could not remove the old void-relay CA")
		manual := "security delete-certificate -c void-relay-local-ca -t " + kc
		if !strings.Contains(r.combined, manual) {
			t.Errorf("the run does not print the manual command %q:\n%s", manual, r.combined)
		}
		if strings.Contains(r.combined, "==> removed the old void-relay CA") {
			t.Errorf("the run claims a removal that did not happen:\n%s", r.combined)
		}
	})
}

func TestShellInstallerRemovesOldRelayCAFromLinuxAnchors(t *testing.T) {
	skipInstallShOnWindows(t)
	if testing.Short() {
		t.Skip("runs the shell installer with command fixtures")
	}

	for _, tc := range []struct {
		name, anchor, refresh string
	}{
		{"debian", "usr/local/share/ca-certificates/void-relay-ca.crt", "update-ca-certificates --fresh"},
		{"rhel", "etc/pki/ca-trust/source/anchors/void-relay-ca.pem", "update-ca-trust extract"},
	} {
		run := func(t *testing.T, sudoMode string) (mirrorResult, string) {
			var anchor string
			r := runMirrorInstall(t, mirrorOpts{
				primary: "ok",
				uname:   "Linux",
				fakes: map[string]string{
					"sudo":                   fakeSudoScript,
					"update-ca-certificates": fakeLoggerScript,
					"update-ca-trust":        fakeLoggerScript,
				},
				env: []string{"FAKE_SUDO=" + sudoMode},
				prepare: func(_, sysRoot string) {
					anchor = filepath.Join(sysRoot, tc.anchor)
					if err := os.MkdirAll(filepath.Dir(anchor), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(anchor, []byte("old CA"), 0o644); err != nil {
						t.Fatal(err)
					}
				},
			})
			if r.code != 0 {
				t.Fatalf("installer exited %d\n%s", r.code, r.combined)
			}
			return r, anchor
		}

		t.Run(tc.name+": removed with sudo, after saying why", func(t *testing.T) {
			r, anchor := run(t, "ok")

			if _, err := os.Stat(anchor); !os.IsNotExist(err) {
				t.Errorf("the old CA anchor is still at %s", anchor)
			}
			if calls := mirrorLogLines(r.log, "sudo rm -f "+anchor); len(calls) != 1 {
				t.Errorf("want one sudo rm of the anchor, log:\n%s", strings.Join(r.log, "\n"))
			}
			if calls := mirrorLogLines(r.log, "sudo "+tc.refresh); len(calls) != 1 {
				t.Errorf("want one sudo %s, log:\n%s", tc.refresh, strings.Join(r.log, "\n"))
			}
			requireBefore(t, r.combined, "removing the old void-relay CA from the system trust store", "<<SUDO PROMPT>>")
			requireBefore(t, r.combined, "sudo may ask for your password", "<<SUDO PROMPT>>")
			requireBefore(t, r.combined, "<<SUDO PROMPT>>", "removed the old void-relay CA")
		})

		t.Run(tc.name+": refused sudo leaves the install whole and says how to finish", func(t *testing.T) {
			r, anchor := run(t, "refuse")

			if _, err := os.Stat(anchor); err != nil {
				t.Fatalf("fixture: the refused sudo removed the anchor anyway")
			}
			if _, err := os.Stat(r.vcPath); err != nil {
				t.Errorf("vc is not installed after a refused CA removal: %v", err)
			}
			requireBefore(t, r.combined, "<<SUDO PROMPT>>", "could not remove the old void-relay CA")
			manual := "sudo rm " + anchor + " && sudo " + tc.refresh
			if !strings.Contains(r.combined, manual) {
				t.Errorf("the run does not print the manual command %q:\n%s", manual, r.combined)
			}
		})
	}
}

func TestShellInstallerDryRunPlansOldRelayCARemoval(t *testing.T) {
	skipInstallShOnWindows(t)

	root := t.TempDir()
	anchor := filepath.Join(root, "usr/local/share/ca-certificates/void-relay-ca.crt")
	if err := os.MkdirAll(filepath.Dir(anchor), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(anchor, []byte("old CA"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "uname"), []byte(fakeUnameScript), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "install.sh", "--dry-run")
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir(), "VC_AUTH_HOST=http://127.0.0.1:1",
		"VC_TEST_ROOT="+root, "FAKE_UNAME_S=Linux", "PATH="+bin+":"+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "WOULD: remove the old void-relay CA") {
		t.Errorf("dry-run does not plan the removal:\n%s", out)
	}
	if _, err := os.Stat(anchor); err != nil {
		t.Errorf("dry-run touched the anchor: %v", err)
	}
}

// Windows: a PowerShell stand-in for Cert:\CurrentUser\Root holding the old CA
// and one unrelated root. Remove-Item prints a marker where Windows shows its
// confirmation dialog, and fails when FAKE_CERT_REMOVE=refuse (the user said No).
const winCertStorePrelude = `
$global:fakeRoot = [System.Collections.ArrayList]::new()
[void]$global:fakeRoot.Add([pscustomobject]@{ Subject = 'CN=void-relay-local-ca'; Thumbprint = '` + oldRelayCASHA1 + `' })
[void]$global:fakeRoot.Add([pscustomobject]@{ Subject = 'CN=Some Other Root'; Thumbprint = 'AAAA' })
function global:Test-Path {
    if ("$args" -like '*Cert:*') { return $true }
    Microsoft.PowerShell.Management\Test-Path @args
}
function global:Get-ChildItem {
    if ("$args" -like '*Cert:*') { return $global:fakeRoot.ToArray() }
    Microsoft.PowerShell.Management\Get-ChildItem @args
}
function global:Remove-Item {
    if ("$args" -like '*Cert:*') {
        [Console]::Out.WriteLine("<<CONFIRM DIALOG>> Remove-Item $args")
        [Console]::Out.Flush()
        if ($env:FAKE_CERT_REMOVE -eq 'refuse') { throw 'The operation was canceled by the user.' }
        $thumb = ("$args" -split '\\')[-1].Split(' ')[0]
        $hit = @($global:fakeRoot | Where-Object { $_.Thumbprint -eq $thumb })
        foreach ($c in $hit) { $global:fakeRoot.Remove($c) }
        return
    }
    Microsoft.PowerShell.Management\Remove-Item @args
}
`

func TestPowerShellInstallerRemovesOldRelayCA(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the PowerShell installer against a local fixture host")
	}

	t.Run("removed, after saying why, and nothing else", func(t *testing.T) {
		r := runWindowsInstall(t, winOpts{sums: "ok", prelude: winCertStorePrelude})
		if r.code != 0 {
			t.Fatalf("installer exited %d\n%s", r.code, r.combined)
		}
		if n := strings.Count(r.combined, "<<CONFIRM DIALOG>>"); n != 1 {
			t.Errorf("want one removal, got %d:\n%s", n, r.combined)
		}
		if !strings.Contains(r.combined, `Cert:\CurrentUser\Root\`+oldRelayCASHA1) {
			t.Errorf("the removal did not name the old CA by thumbprint:\n%s", r.combined)
		}
		requireBefore(t, r.combined, "removing the old void-relay CA from your trusted root certificates", "<<CONFIRM DIALOG>>")
		requireBefore(t, r.combined, "Windows will ask you to confirm", "<<CONFIRM DIALOG>>")
		requireBefore(t, r.combined, "<<CONFIRM DIALOG>>", "removed the old void-relay CA")
	})

	t.Run("a declined dialog leaves the install whole and says how to finish", func(t *testing.T) {
		r := runWindowsInstall(t, winOpts{sums: "ok", prelude: winCertStorePrelude,
			env: []string{"FAKE_CERT_REMOVE=refuse"}})
		if r.code != 0 {
			t.Fatalf("installer exited %d\n%s", r.code, r.combined)
		}
		if _, err := os.Stat(r.vcPath); err != nil {
			t.Errorf("vc.exe is not installed after a declined CA removal: %v", err)
		}
		requireBefore(t, r.combined, "<<CONFIRM DIALOG>>", "could not remove the old void-relay CA")
		manual := `Get-ChildItem Cert:\CurrentUser\Root | Where-Object Subject -eq 'CN=void-relay-local-ca' | Remove-Item`
		if !strings.Contains(r.combined, manual) {
			t.Errorf("the run does not print the manual command %q:\n%s", manual, r.combined)
		}
		if strings.Contains(r.combined, "==> removed the old void-relay CA") {
			t.Errorf("the run claims a removal that did not happen:\n%s", r.combined)
		}
	})
}
