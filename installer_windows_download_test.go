package installercontract

// install.ps1: the download the Windows user actually gets.
//
// The Windows installer is a generation behind the shell one, and the gap that
// matters most is not a missing check — it is a check that is announced and not
// performed. install.ps1 prints
//
//     ==> verifying download (unsigned preview — minisign in future release)
//
// and the only thing standing between that line and the bytes it is talking
// about is `if ($tmpSize -lt 1024)`. The parenthetical names a SIGNATURE, so a
// reader concludes that integrity is covered and only authenticity is pending.
// It is not a hole in the защита, it is a wrong label on it, and the label is
// why nobody went looking.
//
// Three things have to become true (spec §«Что должно стать правдой» 1-3):
//
//   1. the verifying line says exactly what was done, or is gone;
//   2. vc.exe is checked against $AUTH_HOST/vc/SHA256SUMS — mismatch refuses,
//      a missing list is said out loud and the install continues (the same
//      transitional rule install.sh got in PR #33, for the same reason: until
//      the next stable release there is no list on the host for anyone);
//   3. a torn download is survived by retrying, not by dying.
//
// A fourth lives in the spec's «Дополнение» and is about the file rather than
// its behaviour: Windows PowerShell 5.1 — the only PowerShell on a stock
// Windows 11 — reads a BOM-less .ps1 from disk as ANSI, and this file is UTF-8
// without a BOM with a long dash in it. It is covered in
// TestPowerShellInstallerIsReadableByWindowsPowerShell51 below.
//
// The mirror (spec §4) is deliberately NOT here: it is a separate spec.
//
// ── how these tests observe behaviour ───────────────────────────────────────
//
// The shell suite fakes `curl` on PATH. That seam does not exist here:
// install.ps1 fetches with Invoke-WebRequest, which is not a program on PATH.
// The seam that does exist is VC_AUTH_HOST — the installer's own knob, already
// used by the e2e harness — so the fixtures are served by an httptest server on
// 127.0.0.1 and the installer is pointed at it. Nothing in this file opens a
// socket to auth.makscee.ru, and every request the run makes is recorded, so an
// assertion about WHERE the run went is evidence rather than inference.
//
// Isolation, same contract as installer_mirror_test.go: the environment is built
// from scratch rather than inherited, USERPROFILE/HOME/TMPDIR live in the test's
// own temp dir, no agent CLI is selected (so node/npm/winget are never reached),
// and VC_TRUST_RELAY_CA is unset (so nothing is imported into a trust store).
//
// ── why the runs are skipped on Windows ─────────────────────────────────────
//
// install.ps1 writes the user's PATH with
// [Environment]::SetEnvironmentVariable('PATH', …, 'User'). On Unix that scope
// is a no-op, which is exactly what makes running the real installer here safe.
// On Windows it is a write to HKCU, and USERPROFILE cannot redirect it: a `go
// test` on a developer's Windows box would permanently append a temp directory
// to their PATH. So these runs skip there and say so, rather than pass by doing
// damage. The static test (requirement 4) runs everywhere.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
)

const (
	// Not a real 0.2.x version: no assertion here can pass against something
	// that happens to be on the machine already.
	winFixtureVersion = "9.9.9"
	// install.ps1 is single-platform by construction: it always asks for the
	// windows/amd64 artifact. The name is the one the release publishes and the
	// one a SHA256SUMS line must therefore carry.
	winAssetName    = "vc-windows-amd64.exe"
	winArtifactPath = "bin/" + winAssetName
	// Where the list has to live: beside version.json, on the host the bytes
	// came from. Same route as install.sh (installer_primary_checksum_test.go),
	// because release.yml publishes exactly one file for both installers.
	winSumsRoute = "/vc/SHA256SUMS"
)

// The served binary, and what a torn transfer leaves behind: a prefix of it.
// A prefix rather than a marker of its own on purpose — truncation is how this
// failure really arrives, and a test whose partial bytes were distinguishable
// by content would let an implementation that installs half a binary look fine.
var (
	winPrimaryBytes = "PRIMARY-" + strings.Repeat("p", 4096) + "\n"
	winPartialBytes = winPrimaryBytes[:700]
)

func winSHA256(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// PowerShell renders errors with ANSI colour even when redirected; assertions
// about what the user is told must not trip over escape codes.
var winANSI = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// ── harness ──────────────────────────────────────────────────────────────────

type winOpts struct {
	// $AUTH_HOST/vc/SHA256SUMS: ok | missing | mismatch | noentry
	sums string
	// the binary: ok | flaky
	primary string
	// how many binary attempts are torn before one succeeds (flaky only)
	flakyN int
	// pre-create $USERPROFILE/.void-code/bin/vc.exe with this content
	existingVC string

	// ── the host answers, rather than the stream breaking ──────────────────
	//
	// A torn stream and an HTTP status are different events, and the whole
	// second half of this file exists because an installer that cannot tell
	// them apart is wrong in one direction or the other. These fields spell
	// the answers out one attempt at a time, so a test says exactly what the
	// host did on attempt 1, on attempt 2, and so on.
	//
	// primaryStatuses[i] is the status returned to attempt i+1 for the binary;
	// once the list runs out the bytes are served. sumsStatuses does the same
	// for the checksum list, ahead of whatever `sums` says.
	primaryStatuses []int
	sumsStatuses    []int
	// A status the host returns to EVERY attempt for the binary — a verdict
	// that will not change however many times it is asked (403, 404).
	primaryStatusAlways int
	// The same for the checksum list, and needed for a different reason: a
	// transient answer that never stops being transient is how "the list is
	// there and we could not get it" actually arrives. Spelling it as "every
	// attempt" rather than as a list of three keeps the fixture true whatever
	// retry budget the installer settles on.
	sumsStatusAlways int
	// The checksum list answers 200, promises a length, delivers a prefix and
	// drops the connection — the torn transfer of requirement 3, met on the file
	// that decides whether the binary gets checked at all. Ends the same way as
	// sumsStatusAlways (nothing to check against) by a route the host never
	// named with a status code.
	sumsTorn bool
}

type winResult struct {
	code     int
	combined string
	// every request the run made, as "GET /vc/…"
	requests []string
	home     string
	vcPath   string
	binDir   string
	tmpDir   string
	sumsURL  string
}

// winPowerShell finds an interpreter, or skips. Same shape as the existing
// TestPowerShellDryRunExitsBeforeWrites: a developer's macOS box usually has no
// pwsh, and a suite that failed there would be reporting on the toolchain
// rather than on install.ps1. GitHub's ubuntu-latest image ships pwsh, so this
// skip is not where these tests spend their life.
func winPowerShell(t *testing.T) string {
	t.Helper()
	if p, err := exec.LookPath("pwsh"); err == nil {
		return p
	}
	if p, err := exec.LookPath("powershell"); err == nil {
		return p
	}
	t.Skip("НЕ СМОГ: PowerShell is not installed, so install.ps1 cannot be run here")
	return ""
}

func winRequestsFor(r winResult, path string) []string {
	var out []string
	for _, line := range r.requests {
		if strings.Contains(line, path) {
			out = append(out, line)
		}
	}
	return out
}

// winVerifyLines returns the lines the run printed about verification. The whole
// of requirement 1 is that these differ between a run that checked something and
// a run that could not, so they are extracted rather than matched.
func winVerifyLines(r winResult) []string {
	var out []string
	for _, line := range strings.Split(r.combined, "\n") {
		if strings.Contains(strings.ToLower(line), "verif") {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return out
}

func runWindowsInstall(t *testing.T, o winOpts) winResult {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("running install.ps1 on Windows would write the real HKCU PATH; USERPROFILE cannot redirect that")
	}
	ps := winPowerShell(t)

	root := t.TempDir()
	home := filepath.Join(root, "home")
	appData := filepath.Join(root, "appdata")
	tmp := filepath.Join(root, "tmp")
	for _, d := range []string{home, appData, tmp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	binDir := filepath.Join(home, ".void-code", "bin")
	if o.existingVC != "" {
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(binDir, "vc.exe"), []byte(o.existingVC), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	sumsMode := o.sums
	if sumsMode == "" {
		// Default "missing" and not "ok", exactly as the shell harness defaults:
		// today the route is a 404 on the real host, and a harness whose silent
		// default was a healthy list would let a caller that never thought about
		// this route pass as though it had.
		sumsMode = "missing"
	}
	primaryMode := o.primary
	if primaryMode == "" {
		primaryMode = "ok"
	}
	flakyN := o.flakyN
	if flakyN == 0 {
		flakyN = 2
	}

	var mu sync.Mutex
	var requests []string
	attempts := 0
	sumsAttempts := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()

		switch r.URL.Path {
		case "/vc/version.json":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"version":%q,"tag":"v%s","artifacts":{"windows/amd64":%q}}`,
				winFixtureVersion, winFixtureVersion, winArtifactPath)

		case winSumsRoute:
			mu.Lock()
			sumsAttempts++
			m := sumsAttempts
			mu.Unlock()
			if o.sumsStatusAlways != 0 {
				http.Error(w, http.StatusText(o.sumsStatusAlways), o.sumsStatusAlways)
				return
			}
			if o.sumsTorn {
				full := winSHA256(winPrimaryBytes) + "  " + winAssetName + "\n"
				w.Header().Set("Content-Length", fmt.Sprint(len(full)))
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(full[:20]))
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				panic(http.ErrAbortHandler)
			}
			if m <= len(o.sumsStatuses) {
				http.Error(w, http.StatusText(o.sumsStatuses[m-1]), o.sumsStatuses[m-1])
				return
			}
			// The release runs `sha256sum vc-* version.json` inside dist/, so the
			// names in the list are bare basenames even though the bytes are
			// served from /vc/bin/. A decoy line first: the checker has to match
			// the asset, not line 1.
			decoy := winSHA256("decoy") + "  vc-somewhere-else\n"
			version := winSHA256("version.json") + "  version.json\n"
			switch sumsMode {
			case "ok":
				fmt.Fprint(w, decoy+winSHA256(winPrimaryBytes)+"  "+winAssetName+"\n"+version)
			case "mismatch":
				fmt.Fprint(w, decoy+strings.Repeat("0", 64)+"  "+winAssetName+"\n"+version)
			case "noentry":
				// A list that exists and simply does not name this asset —
				// indistinguishable from an older format of the same file, which
				// is why it gets the missing-list treatment and not a refusal.
				fmt.Fprint(w, decoy+version)
			default:
				http.NotFound(w, r)
			}

		case "/vc/" + winArtifactPath:
			mu.Lock()
			attempts++
			n := attempts
			mu.Unlock()
			if o.primaryStatusAlways != 0 {
				http.Error(w, http.StatusText(o.primaryStatusAlways), o.primaryStatusAlways)
				return
			}
			if n <= len(o.primaryStatuses) {
				http.Error(w, http.StatusText(o.primaryStatuses[n-1]), o.primaryStatuses[n-1])
				return
			}
			if primaryMode == "flaky" && n <= flakyN {
				// A torn transfer, not a 404: the promised length arrives in the
				// header, a prefix of the body arrives on the wire, and the
				// connection dies. This is the failure a single Invoke-WebRequest
				// cannot survive and the one the client reported as curl: (92).
				w.Header().Set("Content-Length", fmt.Sprint(len(winPrimaryBytes)))
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(winPartialBytes))
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				panic(http.ErrAbortHandler)
			}
			_, _ = w.Write([]byte(winPrimaryBytes))

		case "/vc/relay-ca.pem":
			fmt.Fprint(w, "-----BEGIN CERTIFICATE-----\nZmFrZQ==\n-----END CERTIFICATE-----\n")

		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// Built from scratch, not from os.Environ(): the real home and the real host
	// must not be able to reach the script even by accident. PATH carries the
	// interpreter's own directory and nothing of this machine's toolchain.
	env := []string{
		"PATH=" + filepath.Dir(ps) + ":/usr/bin:/bin",
		"HOME=" + home,
		"USERPROFILE=" + home,
		"APPDATA=" + appData,
		"TMPDIR=" + tmp,
		"TEMP=" + tmp,
		"TMP=" + tmp,
		"POWERSHELL_TELEMETRY_OPTOUT=1",
		"POWERSHELL_UPDATECHECK=Off",
		"DOTNET_CLI_TELEMETRY_OPTOUT=1",
		"VC_AUTH_HOST=" + srv.URL,
		"VC_LANG=en",
		"VC_INSTALL_PI=0",
		"VC_INSTALL_CLAUDE=0",
		"VC_INSTALL_CODEX=0",
	}

	cmd := exec.Command(ps, "-NoProfile", "-File", "install.ps1")
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("could not run install.ps1: %v\n%s", err, output)
		}
		code = exitErr.ExitCode()
	}

	mu.Lock()
	recorded := append([]string(nil), requests...)
	mu.Unlock()

	return winResult{
		code:     code,
		combined: winANSI.ReplaceAllString(string(output), ""),
		requests: recorded,
		home:     home,
		vcPath:   filepath.Join(binDir, "vc.exe"),
		binDir:   binDir,
		tmpDir:   tmp,
		sumsURL:  srv.URL + winSumsRoute,
	}
}

func winReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// winFilesContaining lists the files under dir whose content is exactly s.
// Used to ask "were the rejected bytes left anywhere" — a refusal that parks the
// unverified download beside vc.exe, or forgets it in TMPDIR, is not a refusal.
func winFilesContaining(t *testing.T, dir, s string) []string {
	t.Helper()
	var out []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		if string(data) == s {
			out = append(out, entry.Name())
		}
	}
	return out
}

// ── requirements 1 and 2 ─────────────────────────────────────────────────────

func TestPowerShellInstallerChecksTheDownloadAgainstSHA256SUMS(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the PowerShell installer against a local fixture host")
	}

	// requirement 2 — the list agrees: the install goes through, and the list was
	// really fetched. Without the second half this subtest passes on an
	// implementation that checks nothing at all.
	t.Run("a download whose checksum agrees installs normally", func(t *testing.T) {
		r := runWindowsInstall(t, winOpts{sums: "ok"})

		if r.code != 0 {
			t.Fatalf("installer exited %d on a download whose sha256 matches\n%s", r.code, r.combined)
		}
		if calls := winRequestsFor(r, winSumsRoute); len(calls) == 0 {
			t.Fatalf("no checksum list was fetched from %s — nothing checked the download\nrequests:\n%s",
				winSumsRoute, strings.Join(r.requests, "\n"))
		}
		if got := winReadFile(t, r.vcPath); got != winPrimaryBytes {
			t.Errorf("vc.exe does not hold the served bytes: got %.32q…", got)
		}
	})

	// requirement 2 — the list disagrees: refuse, and leave the working install
	// alone. The existing vc.exe is the witness; it must survive byte for byte,
	// and the rejected bytes must not be parked next to it or left in TMPDIR.
	t.Run("a download whose checksum disagrees is refused and replaces nothing", func(t *testing.T) {
		sentinel := "SENTINEL-" + strings.Repeat("s", 2048) + "\n"
		r := runWindowsInstall(t, winOpts{sums: "mismatch", existingVC: sentinel})

		if len(winRequestsFor(r, winSumsRoute)) == 0 {
			t.Fatalf("no checksum list was fetched, so no mismatch could have been found\nrequests:\n%s",
				strings.Join(r.requests, "\n"))
		}
		if r.code == 0 {
			t.Errorf("installer exited 0 after a sha256 mismatch\n%s", r.combined)
		}
		if got := winReadFile(t, r.vcPath); got != sentinel {
			t.Errorf("the working vc.exe was replaced with unverified bytes: got %.32q…", got)
		}
		lower := strings.ToLower(r.combined)
		if !strings.Contains(lower, "sha256") && !strings.Contains(lower, "checksum") {
			t.Errorf("the refusal never mentions the checksum, so the user cannot know why:\n%s", r.combined)
		}
		if left := winFilesContaining(t, r.binDir, winPrimaryBytes); len(left) != 0 {
			t.Errorf("rejected bytes were left in the install dir as %s", strings.Join(left, ", "))
		}
		if left := winFilesContaining(t, r.tmpDir, winPrimaryBytes); len(left) != 0 {
			t.Errorf("rejected bytes were left in TMPDIR as %s", strings.Join(left, ", "))
		}
	})

	// requirement 2 — the transitional case, and the one that is easiest to get
	// wrong in the strict direction. Between this change and the next stable
	// release $AUTH_HOST/vc/SHA256SUMS is a 404 for everyone, so a run that
	// refuses here breaks every Windows install there is. Say it and carry on.
	t.Run("a missing checksum list is said out loud and does not stop the install", func(t *testing.T) {
		r := runWindowsInstall(t, winOpts{sums: "missing"})

		if calls := winRequestsFor(r, winSumsRoute); len(calls) == 0 {
			t.Fatalf("the checksum list was never asked for, so its absence was never handled\nrequests:\n%s",
				strings.Join(r.requests, "\n"))
		}
		if r.code != 0 {
			t.Fatalf("installer exited %d because the checksum list is not on the host yet — this breaks every install until the next release\n%s",
				r.code, r.combined)
		}
		if got := winReadFile(t, r.vcPath); got != winPrimaryBytes {
			t.Errorf("vc.exe does not hold the served bytes: got %.32q…", got)
		}
		// The words are the only thing that can carry this case: a run that
		// silently installs unchecked bytes satisfies every other assertion here.
		// One marker, so the sentence around it stays the implementer's to write.
		if !strings.Contains(strings.ToLower(r.combined), "not verified") {
			t.Errorf("the run never says the download was %q — the user is told nothing about installing unchecked bytes:\n%s",
				"not verified", r.combined)
		}
		if !strings.Contains(r.combined, r.sumsURL) {
			t.Errorf("the run never names %s, so the user cannot see which list was missing:\n%s",
				r.sumsURL, r.combined)
		}
	})

	// requirement 2 — a list that exists and does not name this asset. Same
	// treatment as a missing one, and for the same reason: it is indistinguishable
	// from an older format of the file, and "no entry means mismatch" refuses on
	// a list that never promised anything about these bytes.
	t.Run("a checksum list without our asset is said out loud and does not stop the install", func(t *testing.T) {
		r := runWindowsInstall(t, winOpts{sums: "noentry"})

		if calls := winRequestsFor(r, winSumsRoute); len(calls) == 0 {
			t.Fatalf("the checksum list was never asked for\nrequests:\n%s", strings.Join(r.requests, "\n"))
		}
		if r.code != 0 {
			t.Fatalf("installer exited %d on a checksum list that carries no entry for %s\n%s",
				r.code, winAssetName, r.combined)
		}
		if got := winReadFile(t, r.vcPath); got != winPrimaryBytes {
			t.Errorf("vc.exe does not hold the served bytes: got %.32q…", got)
		}
		if !strings.Contains(strings.ToLower(r.combined), "not verified") {
			t.Errorf("a list without our entry checked nothing, and the run does not say so:\n%s", r.combined)
		}
	})

	// requirement 1 — the label matches what happened.
	//
	// Stated as a difference between two runs rather than as a pattern in the
	// source, because that is precisely the defect: today the identical sentence
	// is printed whether the bytes were checked against a list or nothing was
	// checked at all. Whatever wording the implementer chooses, a run that
	// verified something and a run that could not cannot say the same thing.
	t.Run("the verifying line says what was actually done", func(t *testing.T) {
		checked := runWindowsInstall(t, winOpts{sums: "ok"})
		unchecked := runWindowsInstall(t, winOpts{sums: "missing"})

		checkedLines := winVerifyLines(checked)
		uncheckedLines := winVerifyLines(unchecked)

		if strings.Join(checkedLines, "\n") == strings.Join(uncheckedLines, "\n") {
			t.Errorf("the run says the same thing about verification whether it checked a list or found none — the label does not describe what happened:\n  with a list:    %s\n  without a list: %s",
				strings.Join(checkedLines, " | "), strings.Join(uncheckedLines, " | "))
		}
		// And the standing lie by name: a run that checked nothing must not
		// announce a verification in progress. It has "not verified" to print.
		if strings.Contains(strings.ToLower(unchecked.combined), "verifying download") {
			t.Errorf("a run that verified nothing still prints a \"verifying download\" line:\n%s", unchecked.combined)
		}
		// The other half of the same requirement: when a list IS there, the user
		// must be able to see that the checksum is what was checked. Otherwise
		// "говорит ровно то, что сделано" is satisfied by deleting the line and
		// never telling anyone the download is now verified.
		lower := strings.ToLower(checked.combined)
		if !strings.Contains(lower, "sha256") && !strings.Contains(lower, "checksum") {
			t.Errorf("a run that verified the checksum never mentions it:\n%s", checked.combined)
		}
	})
}

// ── requirement 3 ────────────────────────────────────────────────────────────

func TestPowerShellInstallerSurvivesATornDownload(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the PowerShell installer against a local fixture host")
	}

	// The host promises a length, delivers a prefix and drops the connection —
	// the Windows shape of the failure the client reported as curl: (92). A
	// single Invoke-WebRequest turns it into a dead installer; only another
	// attempt can rescue it.
	r := runWindowsInstall(t, winOpts{primary: "flaky", flakyN: 2, sums: "ok"})

	if r.code != 0 {
		t.Fatalf("installer exited %d on a host that delivers on the third attempt\n%s", r.code, r.combined)
	}
	calls := winRequestsFor(r, "/vc/"+winArtifactPath)
	if len(calls) < 2 {
		t.Fatalf("the vc.exe download was attempted %d time(s) — a single shot is not a retry\nrequests:\n%s",
			len(calls), strings.Join(r.requests, "\n"))
	}
	got := winReadFile(t, r.vcPath)
	if got == winPartialBytes {
		t.Fatalf("a truncated download was installed as vc.exe (%d bytes)", len(got))
	}
	if got != winPrimaryBytes {
		t.Fatalf("vc.exe does not hold the served bytes: %d bytes, %.32q…", len(got), got)
	}
}

// ── requirement 4: the file has to be readable by Windows PowerShell 5.1 ─────
//
// Measured on WIN11-VCLAB (10.10.10.32), where $PSVersionTable.PSVersion is
// 5.1.26100.9168 and `Get-Command pwsh` is False — a stock Windows 11 has no
// PowerShell 7 at all:
//
//   irm https://auth.makscee.ru/vc/install.ps1 | iex   →  parses fine
//   download the file, run it from disk                →  25 parse errors,
//                                                         first: Missing closing '}'
//
// The two halves differ because the server sends
// `content-type: text/x-powershell; charset=utf-8` and Invoke-RestMethod decodes
// by that header. From disk there is no header, and 5.1 reads a BOM-less .ps1 as
// ANSI — so the UTF-8 bytes of the long dash in this file decode into a smart
// quote, which opens a string that never closes. The documented path works; the
// natural one, and the only one left when corporate policy forbids `iex` from
// the network, does not.
//
// Which of the spec's two fixes is available is a measurement, not a preference.
// What the file actually holds outside ASCII (counted over install.ps1 at
// e4b3102 + PR #33): 13 em dashes, 4 ellipses, and the Cyrillic of its own
// language menu — «Выберите язык» and «Русский». So "replace the long dashes
// with ASCII" does not finish the job here; the BOM does, and it also covers
// whatever non-ASCII is added tomorrow.

// win51DecodeFromDisk reproduces the decision Windows PowerShell 5.1 makes when
// it opens a .ps1 from disk: a BOM decides the encoding, and without one the
// bytes are a single-byte ANSI codepage. CP1252 stands in for that codepage —
// this file's failure does not depend on which one it is (0x94 is a closing
// smart quote in 1252 and in 1251 alike), and the fix does not either: a BOM
// removes the question, and pure ASCII makes every codepage agree.
func win51DecodeFromDisk(data []byte) (text string, hadBOM bool) {
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		return string(data[3:]), true
	}
	if len(data) >= 2 && ((data[0] == 0xFF && data[1] == 0xFE) || (data[0] == 0xFE && data[1] == 0xFF)) {
		// A UTF-16 BOM is also honoured by 5.1; not what this file uses, but a
		// decoder that pretended otherwise would misreport a legitimate fix.
		return "", true
	}
	var b strings.Builder
	for _, c := range data {
		if c < 0x80 {
			b.WriteByte(c)
			continue
		}
		if r, ok := cp1252High[c]; ok {
			b.WriteRune(r)
			continue
		}
		b.WriteRune(rune(c)) // 0xA0-0xFF are Latin-1 code points
	}
	return b.String(), false
}

// The only part of CP1252 that is not Latin-1. 0x81, 0x8D, 0x8F, 0x90 and 0x9D
// are undefined and fall through to the Latin-1 branch, which is close enough:
// they are unprintable either way.
var cp1252High = map[byte]rune{
	0x80: '€', 0x82: '‚', 0x83: 'ƒ', 0x84: '„', 0x85: '…',
	0x86: '†', 0x87: '‡', 0x88: 'ˆ', 0x89: '‰', 0x8A: 'Š',
	0x8B: '‹', 0x8C: 'Œ', 0x8E: 'Ž', 0x91: '‘', 0x92: '’',
	0x93: '“', 0x94: '”', 0x95: '•', 0x96: '–', 0x97: '—',
	0x98: '˜', 0x99: '™', 0x9A: 'š', 0x9B: '›', 0x9C: 'œ',
	0x9E: 'ž', 0x9F: 'Ÿ',
}

func TestPowerShellInstallerIsReadableByWindowsPowerShell51(t *testing.T) {
	data, err := os.ReadFile("install.ps1")
	if err != nil {
		t.Fatalf("read install.ps1: %v", err)
	}

	// Part one: a byte-level property, and stated as one on purpose. Whether 5.1
	// can parse a file is 5.1's business, and 5.1 exists only on Windows; what
	// can be measured on any machine is the input it is handed. Without a BOM,
	// every non-ASCII byte in this file reaches 5.1 as a different character than
	// it reaches every other reader — so either the BOM is there, or nothing
	// non-ASCII is.
	t.Run("the bytes on disk decode the same for 5.1 as for everyone else", func(t *testing.T) {
		hasBOM := len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF
		if hasBOM {
			return
		}
		var offenders []string
		line := 1
		for i, c := range data {
			if c == '\n' {
				line++
			}
			if c < 0x80 {
				continue
			}
			if len(offenders) < 5 {
				offenders = append(offenders, fmt.Sprintf("line %d, byte %d: 0x%02X", line, i, c))
			}
		}
		if len(offenders) > 0 {
			decoded, _ := win51DecodeFromDisk(data)
			t.Errorf("install.ps1 has no UTF-8 BOM and is not pure ASCII (%d non-ASCII bytes), so Windows PowerShell 5.1 — the only PowerShell on a stock Windows 11 — reads it from disk as ANSI and gets different text:\n  %s\n\nfirst 5.1 sees: %.60q\nfix: a UTF-8 BOM at the start of the file. The other candidate the spec names — ASCII-only punctuation — does not reach here: the file also carries the Cyrillic of its own language menu (Выберите язык / Русский), so ASCII-only would mean deleting that menu",
				len(data)-countASCII(data), strings.Join(offenders, "\n  "), firstDifference(string(data), decoded))
		}
	})

	// Part two: the same property as behaviour, for machines that have a
	// PowerShell to ask. The text 5.1 would see is reconstructed from the bytes
	// and handed to the parser — this reproduces the 25 errors from WIN11-VCLAB
	// on any platform, which is more than a byte count can claim.
	t.Run("the text 5.1 would read from disk still parses", func(t *testing.T) {
		ps := winPowerShell(t)

		text, hadBOM := win51DecodeFromDisk(data)
		if hadBOM && text == "" {
			t.Skip("НЕ СМОГ: install.ps1 is UTF-16; this check reconstructs 8-bit reads only")
		}

		dir := t.TempDir()
		script := filepath.Join(dir, "as-51-reads-it.ps1")
		if err := os.WriteFile(script, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		parser := filepath.Join(dir, "parse.ps1")
		if err := os.WriteFile(parser, []byte(`
$errs = $null
[void][System.Management.Automation.Language.Parser]::ParseFile($env:PARSE_TARGET, [ref]$null, [ref]$errs)
if ($errs -and $errs.Count -gt 0) {
    Write-Output "PARSE ERRORS: $($errs.Count)"
    $errs | Select-Object -First 5 | ForEach-Object {
        Write-Output ("  line {0}: {1}" -f $_.Extent.StartLineNumber, $_.Message)
    }
    exit 1
}
Write-Output "PARSE OK"
`), 0o644); err != nil {
			t.Fatal(err)
		}

		cmd := exec.Command(ps, "-NoProfile", "-File", parser)
		cmd.Env = []string{
			"PATH=" + filepath.Dir(ps) + ":/usr/bin:/bin",
			"HOME=" + dir,
			"PARSE_TARGET=" + script,
			"POWERSHELL_TELEMETRY_OPTOUT=1",
			"POWERSHELL_UPDATECHECK=Off",
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Errorf("the text Windows PowerShell 5.1 reads from this file does not parse — downloading install.ps1 and running it is the path the download page teaches, and the path left when policy forbids `iex` from the network:\n%s",
				winANSI.ReplaceAllString(string(out), ""))
		}
	})
}

func countASCII(data []byte) int {
	n := 0
	for _, c := range data {
		if c < 0x80 {
			n++
		}
	}
	return n
}

// firstDifference returns a window of the ANSI-decoded text around the first
// place it stops agreeing with the UTF-8 one — the concrete character that turns
// into a stray quote, rather than a claim that one exists.
func firstDifference(utf8Text, ansiText string) string {
	a := []rune(utf8Text)
	b := []rune(ansiText)
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			start := i - 20
			if start < 0 {
				start = 0
			}
			end := i + 20
			if end > len(b) {
				end = len(b)
			}
			return string(b[start:end])
		}
	}
	return ""
}

// ── transient answers versus verdicts ────────────────────────────────────────
//
// Requirement 3 above proves the installer survives a BROKEN STREAM. It says
// nothing about the other half of the same question: what the installer does
// when the host does answer, with a number.
//
// The two are not the same event and must not get the same treatment:
//
//	502, 503, 429, 408, and a timeout  — "not now". curl retries these; the
//	     man page names exactly this set as transient, and install.sh widens it
//	     further with --retry-all-errors (lines 101-106). A single 502 during a
//	     deploy is the most ordinary thing there is on a host being deployed to.
//	403, 404, 401                      — "no". The same answer three times, so
//	     asking twice more buys nothing. $AUTH_HOST/vc/SHA256SUMS is a 404 on
//	     every host until the next stable release, and every install in the
//	     world would otherwise sit through a retry budget for it.
//
// The Windows installer currently has one bucket for both: any answer at all
// ends the attempts. So a deploy-time 502 kills a Windows install that the
// shell path shrugs off — and no test in this file could see it, because the
// fixtures only knew how to break a stream, never how to answer.
//
// ── what these assertions are made of, and why that matters here ─────────────
//
// Only two observables: how many requests the host received, and how the run
// ended (exit code, bytes at ~/.void-code/bin/vc.exe, what the user was told).
// Nothing here asserts how the implementation recognises a retryable failure.
//
// That restraint is not stylistic. Every run in this file executes under
// PowerShell 7, and the machine these installs happen on runs Windows
// PowerShell 5.1, where Invoke-WebRequest throws a WebException rather than an
// HttpResponseException — so any assertion phrased in terms of the exception
// object would be pinning a 7-shaped detail that 5.1 does not have. Counting
// requests and reading the outcome is the same statement under both, which is
// what makes these tests worth anything for the machine they are about. What
// they still cannot do is RUN 5.1: see the note at the end of this file.
func TestPowerShellInstallerRetriesTransientAnswersAndNotVerdicts(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the PowerShell installer against a local fixture host")
	}

	// A host mid-deploy answers 502 once and serves the file on the next ask.
	// The shell installer survives this; today the Windows one exits 1.
	t.Run("a 502 on the first attempt is retried, not surrendered to", func(t *testing.T) {
		r := runWindowsInstall(t, winOpts{primaryStatuses: []int{502}, sums: "ok"})

		calls := winRequestsFor(r, "/vc/"+winArtifactPath)
		if r.code != 0 {
			t.Fatalf("installer exited %d after a single 502 — one bad gateway during a deploy kills the install, while install.sh retries it (%d request(s) made)\n%s",
				r.code, len(calls), r.combined)
		}
		if len(calls) < 2 {
			t.Fatalf("the binary was requested %d time(s): a 502 was never retried\nrequests:\n%s",
				len(calls), strings.Join(r.requests, "\n"))
		}
		if got := winReadFile(t, r.vcPath); got != winPrimaryBytes {
			t.Errorf("vc.exe does not hold the served bytes: got %.32q…", got)
		}
	})

	// A second code, and a different family: 429 is the host saying "slow down",
	// which is the one answer where retrying is not merely allowed but asked for.
	t.Run("a 429 on the first attempt is retried, not surrendered to", func(t *testing.T) {
		r := runWindowsInstall(t, winOpts{primaryStatuses: []int{429}, sums: "ok"})

		calls := winRequestsFor(r, "/vc/"+winArtifactPath)
		if r.code != 0 {
			t.Fatalf("installer exited %d after a single 429 — the host asked to be asked again and was not (%d request(s) made)\n%s",
				r.code, len(calls), r.combined)
		}
		if len(calls) < 2 {
			t.Fatalf("the binary was requested %d time(s): a 429 was never retried\nrequests:\n%s",
				len(calls), strings.Join(r.requests, "\n"))
		}
		if got := winReadFile(t, r.vcPath); got != winPrimaryBytes {
			t.Errorf("vc.exe does not hold the served bytes: got %.32q…", got)
		}
	})

	// The same distinction on the checksum list, where getting it wrong is
	// quieter and therefore worse: a 503 that is not retried does not fail the
	// install, it downgrades it. The user is told the download could not be
	// checked — the truthful sentence for a route that does not exist yet, and a
	// lie about a host that simply hiccuped and had the list all along.
	t.Run("a 503 on the checksum list does not quietly downgrade the install to unverified", func(t *testing.T) {
		r := runWindowsInstall(t, winOpts{sums: "ok", sumsStatuses: []int{503}})

		calls := winRequestsFor(r, winSumsRoute)
		if r.code != 0 {
			t.Fatalf("installer exited %d on a checksum list that answered 503 once\n%s", r.code, r.combined)
		}
		if len(calls) < 2 {
			t.Fatalf("the checksum list was requested %d time(s): a 503 was never retried\nrequests:\n%s",
				len(calls), strings.Join(r.requests, "\n"))
		}
		if strings.Contains(strings.ToLower(r.combined), "not verified") {
			t.Errorf("the list was there and the run still tells the user the download is unverified — a transient answer was taken for a missing route:\n%s",
				r.combined)
		}
		if !strings.Contains(strings.ToLower(r.combined), "sha256") {
			t.Errorf("the run never says the checksum was checked:\n%s", r.combined)
		}
	})

	// ── the other side of the same line, and the reason it is drawn ──────────
	//
	// These two are guards: they pass today, and the fix above is exactly the
	// kind of change that breaks them. Retrying everything is the naive repair,
	// and it costs a retry budget on every single install (the 404 case below is
	// the state of the world for every user until the next stable release).

	t.Run("a 403 on the binary is a verdict and is asked exactly once", func(t *testing.T) {
		sentinel := "SENTINEL-" + strings.Repeat("v", 2048) + "\n"
		r := runWindowsInstall(t, winOpts{primaryStatusAlways: 403, sums: "ok", existingVC: sentinel})

		if r.code == 0 {
			t.Errorf("installer exited 0 though the host refused to serve the binary\n%s", r.combined)
		}
		calls := winRequestsFor(r, "/vc/"+winArtifactPath)
		if len(calls) != 1 {
			t.Errorf("a 403 was asked %d time(s); it is the host's answer and will be the same answer every time\nrequests:\n%s",
				len(calls), strings.Join(r.requests, "\n"))
		}
		if got := winReadFile(t, r.vcPath); got != sentinel {
			t.Errorf("the working vc.exe did not survive a refused download: got %.32q…", got)
		}
	})

	t.Run("the 404 on a checksum list that does not exist yet is asked exactly once", func(t *testing.T) {
		r := runWindowsInstall(t, winOpts{sums: "missing"})

		if r.code != 0 {
			t.Fatalf("installer exited %d with no checksum list on the host\n%s", r.code, r.combined)
		}
		calls := winRequestsFor(r, winSumsRoute)
		if len(calls) != 1 {
			t.Errorf("the missing list was asked for %d time(s) — that is the retry budget every install in the world pays until the next stable release publishes it\nrequests:\n%s",
				len(calls), strings.Join(r.requests, "\n"))
		}
	})
}

// ── "nothing to check it against" is three different facts ───────────────────
//
// The install continues in all three, and that is deliberate — strictness here
// breaks every Windows install until the next stable release publishes the list
// (see the transitional case above). What is asserted below is not the outcome
// but the SENTENCE: today one paragraph is printed for three unrelated events,
// and in two of them it states a cause that is false.
//
//	the host answered 404          — the list does not exist yet, for anyone.
//	     "published from the next stable release onwards" is true here and only
//	     here.
//	the host never delivered it    — 5xx to every attempt, or a torn transfer.
//	     The list may well be published and complete; what happened is that this
//	     run could not get it. After the release this is the shape of a silent
//	     downgrade to an unverified install, and calling it "not published yet"
//	     tells the user to wait for something that already happened.
//	the list had no line for us    — it was fetched and read, and does not name
//	     this asset. Neither "not published" nor "could not reach" is true.
//
// The installer already carries the means to tell them apart:
// $script:VCLastDownloadStatus is the status of the last fetch (0 when the host
// answered nothing at all), and Get-VCSha256Status is the one place that knows
// whether the list was read and searched. Nothing below prescribes which of
// them is used, or what the replacement sentences say — only which fact the
// user is told, and which false ones they are not.
//
// Phrasings are matched as sets for that reason. An assertion pinned to one
// exact sentence would be a spelling test; these ask whether the run makes the
// "not published yet" claim, the "could not reach it" claim, or the "no entry
// for this asset" claim, across the ways each is ordinarily written.

var winNotPublishedYetMarkers = []string{
	"next stable release",
	"not published",
	"not yet published",
	"isn't published",
	"is published from",
	"does not exist yet",
	"doesn't exist yet",
	"no such list",
	"not on the host yet",
}

var winUnreachableMarkers = []string{
	"could not fetch",
	"couldn't fetch",
	"could not be fetched",
	"could not reach",
	"couldn't reach",
	"could not be reached",
	"could not download",
	"couldn't download",
	"failed to fetch",
	"unreachable",
	"did not answer",
	"didn't answer",
	"no answer",
}

var winNoEntryMarkers = []string{
	"lists no",
	"does not list",
	"doesn't list",
	"no entry",
	"not listed",
	"no sha256 for",
	"no line for",
	"has no line",
}

// winSaysAny returns the marker the run's output carries, or "" for none, plus
// the line it was on — a failure message that names the offending sentence is
// worth more than one asserting a boolean.
func winSaysAny(out string, markers []string) (marker, line string) {
	for _, l := range strings.Split(out, "\n") {
		low := strings.ToLower(l)
		for _, m := range markers {
			if strings.Contains(low, m) {
				return m, strings.TrimSpace(l)
			}
		}
	}
	return "", ""
}

// winInstalledCleanly is the half of every case below that must NOT change: the
// bytes are installed and the run ends 0. Stated in each subtest because the
// repair being asked for is a change to wording, and the way wording changes go
// wrong is by turning a warning into a refusal.
func winInstalledCleanly(t *testing.T, r winResult, why string) {
	t.Helper()
	if r.code != 0 {
		t.Fatalf("installer exited %d when %s — the install must continue in every unchecked case, or every user is blocked by a warning\n%s",
			r.code, why, r.combined)
	}
	if got := winReadFile(t, r.vcPath); got != winPrimaryBytes {
		t.Errorf("vc.exe does not hold the served bytes when %s: got %.32q…", why, got)
	}
	if !strings.Contains(strings.ToLower(r.combined), "not verified") {
		t.Errorf("the run never says the download is unverified when %s — the user is told nothing about installing unchecked bytes:\n%s",
			why, r.combined)
	}
}

func TestPowerShellInstallerNamesWhyTheDownloadWasNotChecked(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the PowerShell installer against a local fixture host")
	}

	// The guard, and the only case where today's sentence is true. It passes
	// now; the point of writing it down is that the repair below must not be
	// made by deleting the explanation that is correct here. Until the next
	// stable release this is the state of the world for every install.
	t.Run("a list that is not on the host yet is called exactly that", func(t *testing.T) {
		r := runWindowsInstall(t, winOpts{sums: "missing"})

		if calls := winRequestsFor(r, winSumsRoute); len(calls) == 0 {
			t.Fatalf("the checksum list was never asked for\nrequests:\n%s", strings.Join(r.requests, "\n"))
		}
		winInstalledCleanly(t, r, "the host answers 404 for the checksum list")

		if m, _ := winSaysAny(r.combined, winNotPublishedYetMarkers); m == "" {
			t.Errorf("the host said there is no such list, and the run does not tell the user it is simply not published yet — that is the one case where waiting for the next release is the right advice:\n%s",
				r.combined)
		}
	})

	// Case 2, half one: the list route answers, and keeps answering 5xx. The
	// retries are spent, the run gives up, and the install goes through
	// unverified. Today it explains that by announcing a release that has
	// nothing to do with what happened.
	t.Run("a list the host never delivered is not called unpublished", func(t *testing.T) {
		r := runWindowsInstall(t, winOpts{sums: "ok", sumsStatusAlways: 503})

		calls := winRequestsFor(r, winSumsRoute)
		if len(calls) < 2 {
			t.Fatalf("the checksum list was asked %d time(s) on a 503 — a transient answer was taken for a verdict, so this run never reached the case under test\nrequests:\n%s",
				len(calls), strings.Join(r.requests, "\n"))
		}
		winInstalledCleanly(t, r, "the checksum list answers 503 to every attempt")

		if m, line := winSaysAny(r.combined, winNotPublishedYetMarkers); m != "" {
			t.Errorf("the list was there and answered 503 to every attempt, and the run tells the user it is not published yet (%q, on %q) — after the release that sentence sends a user to wait for something that already shipped, while their install silently went through unverified:\nfull output:\n%s",
				m, line, r.combined)
		}
		if m, _ := winSaysAny(r.combined, winUnreachableMarkers); m == "" {
			t.Errorf("the run never says the checksum list could not be fetched, so the one true fact about this install — nobody got the list — reaches the user in no form at all:\n%s",
				r.combined)
		}
	})

	// Case 2, half two: no status at all. The host accepts, promises a length
	// and dies mid-body — $script:VCLastDownloadStatus is 0 here and 503 above,
	// and the user's sentence must be the same one either way: we could not get
	// it. Written as its own subtest because an implementation that keys only
	// off "the status was 404" passes the 503 half by accident and fails here.
	t.Run("a list torn mid-transfer is not called unpublished", func(t *testing.T) {
		r := runWindowsInstall(t, winOpts{sums: "ok", sumsTorn: true})

		calls := winRequestsFor(r, winSumsRoute)
		if len(calls) < 2 {
			t.Fatalf("the checksum list was asked %d time(s) after a torn transfer — a broken stream is the failure this installer keeps meeting and is retried elsewhere in this file\nrequests:\n%s",
				len(calls), strings.Join(r.requests, "\n"))
		}
		winInstalledCleanly(t, r, "the checksum list is torn mid-transfer on every attempt")

		if m, line := winSaysAny(r.combined, winNotPublishedYetMarkers); m != "" {
			t.Errorf("the transfer of the list broke and the run calls that \"not published yet\" (%q, on %q) — the host answered 200 and started sending it:\nfull output:\n%s",
				m, line, r.combined)
		}
		if m, _ := winSaysAny(r.combined, winUnreachableMarkers); m == "" {
			t.Errorf("the run never says the checksum list could not be fetched after the transfer broke:\n%s", r.combined)
		}
	})

	// Case 3: fetched, read, and it does not name this asset. The list exists
	// and is published, and this run reached it — so both of the other two
	// explanations are false, and the third one is a fact neither of them can
	// carry: the list is not the one these bytes belong to, or not complete.
	t.Run("a list without our entry is called neither unpublished nor unreachable", func(t *testing.T) {
		r := runWindowsInstall(t, winOpts{sums: "noentry"})

		if calls := winRequestsFor(r, winSumsRoute); len(calls) == 0 {
			t.Fatalf("the checksum list was never asked for\nrequests:\n%s", strings.Join(r.requests, "\n"))
		}
		winInstalledCleanly(t, r, "the checksum list carries no entry for "+winAssetName)

		if m, line := winSaysAny(r.combined, winNotPublishedYetMarkers); m != "" {
			t.Errorf("the list was fetched and read, and the run tells the user it is not published yet (%q, on %q) — waiting for the next release fixes nothing here:\nfull output:\n%s",
				m, line, r.combined)
		}
		if m, line := winSaysAny(r.combined, winUnreachableMarkers); m != "" {
			t.Errorf("the list arrived intact, and the run tells the user it could not be fetched (%q, on %q):\nfull output:\n%s",
				m, line, r.combined)
		}
		if m, _ := winSaysAny(r.combined, winNoEntryMarkers); m == "" {
			t.Errorf("the run never says the list carries no entry for %s, so the user cannot tell a stale or wrong list from a missing one:\n%s",
				winAssetName, r.combined)
		}
	})
}

// ── what none of this reaches: Windows PowerShell 5.1 ────────────────────────
//
// Every run above is PowerShell 7. The installs these tests are about happen on
// Windows PowerShell 5.1 — measured on WIN11-VCLAB, where `Get-Command pwsh` is
// False — and the two differ in exactly the area this file now tests: 5.1's
// Invoke-WebRequest throws a WebException carrying an HttpWebResponse, 7 throws
// an HttpResponseException carrying an HttpResponseMessage.
//
// The assertions are written to be indifferent to that (request counts and the
// outcome, never the exception), so they state something true of both. What no
// test here can do is execute 5.1: whether the implementation's own way of
// telling a transient answer from a verdict behaves the same under WebException
// is UNVERIFIED by this suite, and provable only on a Windows runner or by hand
// on WIN11-VCLAB. Said here rather than left to be assumed.
