package installercontract

// The CLI installers must provision exactly the Pi the desktop pins.
//
// install.sh and install.ps1 put @earendil-works/pi-coding-agent into VC's
// managed runtime ($HOME/.void-code/runtime/pi). They used to ask npm for the
// package with no version, so a user got whatever was latest on the registry
// that day — possibly newer than what the vc extension supports — while the
// desktop build pins one exact version in desktop/runtime/pi/package.json.
//
// Three things have to become true:
//
//   1. the installers install exactly the version pinned in
//      desktop/runtime/pi/package.json;
//   2. every npm command for Pi an installer prints (dry-run "WOULD:", "Run
//      manually:", "Run when ready:", NEXT STEPS) names the same version, so a
//      user who copies it gets the pinned Pi;
//   3. a managed runtime that already holds Pi at a DIFFERENT version is
//      reinstalled at the pin instead of being reported "already installed";
//      one that already holds the pin is left alone.
//
// The pin is read from desktop/runtime/pi/package.json at test time and never
// written here. That is the whole point: the installers are served standalone
// (`curl … | sh`), so they must carry the version themselves, and this file is
// what keeps their copy and the desktop's copy from drifting apart.
//
// How the shell installer is observed: a stub `npm` on PATH records its argv
// and, when asked to install Pi, lays down a fake managed Pi whose package.json
// carries the version it was asked for (a registry "latest" when none was
// asked for). The stub `node` answers only `--version`: the installed version
// is expected to be read from the managed package.json text, the way
// codex_installed_version already reads @openai/codex's, not by executing JS.
// `sleep` is stubbed so the npm retry loop costs nothing.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

const piPinPackage = "@earendil-works/pi-coding-agent"

// What the stub npm installs when it is not told a version: an unmistakable
// stand-in for "whatever the registry says is latest".
const piPinRegistryLatest = "9.99.0-registry-latest"

var piPinExactVersionRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)

// piPinFromDesktop returns the Pi version pinned for the desktop runtime.
func piPinFromDesktop(t *testing.T) string {
	t.Helper()
	const path = "desktop/runtime/pi/package.json"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("НЕ СМОГ: cannot read the desktop Pi pin %s: %v", path, err)
	}
	var manifest struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("НЕ СМОГ: %s is not JSON: %v", path, err)
	}
	pin := manifest.Dependencies[piPinPackage]
	if pin == "" {
		t.Fatalf("НЕ СМОГ: %s has no dependencies[%q]; there is no pin to compare the installers against", path, piPinPackage)
	}
	if !piPinExactVersionRe.MatchString(pin) {
		t.Fatalf("НЕ СМОГ: %s pins %s as %q, which is not an exact version; the installers cannot mirror a range", path, piPinPackage, pin)
	}
	return pin
}

func piPinSpec(pin string) string { return piPinPackage + "@" + pin }

// piPinMentionProblems returns every mention of the Pi package in text that is
// not spelled with the pin. A mention followed by '/' or '\' is a filesystem
// path (node_modules/@earendil-works/pi-coding-agent/…) and carries no version.
func piPinMentionProblems(text, pin string) (problems []string, pinned int) {
	want := "@" + pin
	for i := 0; ; {
		j := strings.Index(text[i:], piPinPackage)
		if j < 0 {
			break
		}
		start := i + j
		rest := text[start+len(piPinPackage):]
		i = start + len(piPinPackage)
		if strings.HasPrefix(rest, "/") || strings.HasPrefix(rest, `\`) {
			continue
		}
		if strings.HasPrefix(rest, want) {
			after := rest[len(want):]
			if after == "" || !isVersionChar(after[0]) {
				pinned++
				continue
			}
		}
		lo, hi := start-40, start+len(piPinPackage)+20
		if lo < 0 {
			lo = 0
		}
		if hi > len(text) {
			hi = len(text)
		}
		problems = append(problems, strings.TrimSpace(text[lo:hi]))
	}
	return problems, pinned
}

func isVersionChar(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '.' || c == '-' || c == '+'
}

// ── shell installer harness ──────────────────────────────────────────────────

const piPinCurlScript = `#!/bin/sh
out=
while [ $# -gt 0 ]; do
  if [ "$1" = -o ]; then out=$2; shift 2; continue; fi
  shift
done
[ -z "$out" ] || { mkdir -p "$(dirname "$out")"; printf fixture > "$out"; }
`

const piPinNodeScript = `#!/bin/sh
case "$1" in
  --version|-v) printf 'v22.11.0\n' ;;
  *) exit 1 ;;
esac
`

const piPinRefuserScript = `#!/bin/sh
exit 1
`

const piPinSleepScript = `#!/bin/sh
printf 'sleep %s\n' "$*" >> "$PIPIN_SLEEP_LOG"
exit 0
`

// One line per invocation, every argument in brackets, so the test can compare
// whole arguments rather than substrings of a joined command line.
const piPinNpmScript = `#!/bin/sh
{
  printf 'NPM'
  for a in "$@"; do printf ' [%s]' "$a"; done
  printf '\n'
} >> "$PIPIN_NPM_LOG"

is_install=0
prefix=
pkg=
prev=
for a in "$@"; do
  case "$a" in
    install|i|add) is_install=1 ;;
    --prefix=*) prefix=${a#--prefix=} ;;
    @earendil-works/pi-coding-agent*) pkg=$a ;;
  esac
  [ "$prev" = --prefix ] && prefix=$a
  prev=$a
done

[ "$is_install" = 1 ] || exit 1
[ -n "$pkg" ] || exit 1
[ "$PIPIN_NPM_MODE" = fail ] && exit 1
# A global install (-g, no --prefix) succeeds as real npm's would and touches
# nothing in the managed runtime. install.sh's check_npm_agent reaches one
# after a successful managed Pi install (its "A && B || C && D" parses as
# "((A && B) || C) && D"); rejecting it here would push every run into the
# failure path for a reason that is the stub's, not the installer's.
if [ -z "$prefix" ]; then
  for a in "$@"; do
    case "$a" in -g|--global) exit 0 ;; esac
  done
  exit 1
fi

# install.ps1 spells the runtime 'runtime\pi'; on a Unix host that separator
# must still land in the same directory.
prefix=$(printf '%s' "$prefix" | tr '\\' '/')

case "$pkg" in
  @earendil-works/pi-coding-agent@*) ver=${pkg#@earendil-works/pi-coding-agent@} ;;
  *) ver=$PIPIN_REGISTRY_LATEST ;;
esac
# A registry that answers with something other than what was asked for, and an
# npm that still exits 0: only a check of the result can notice.
[ "$PIPIN_NPM_MODE" = ignore-version ] && ver=$PIPIN_REGISTRY_LATEST

dir="$prefix/node_modules/@earendil-works/pi-coding-agent"
rm -rf "$dir"
mkdir -p "$dir/dist" "$prefix/node_modules/.bin"
printf '{\n  "name": "@earendil-works/pi-coding-agent",\n  "version": "%s",\n  "type": "module"\n}\n' "$ver" > "$dir/package.json"
printf '#!/usr/bin/env node\n' > "$dir/dist/cli.js"
chmod 755 "$dir/dist/cli.js"
# The entrypoint npm generates on Windows, which install.ps1 checks for.
printf '@echo off\n' > "$prefix/node_modules/.bin/pi.cmd"
chmod 755 "$prefix/node_modules/.bin/pi.cmd"
exit 0
`

type piPinOpts struct {
	existingVersion string // pre-create a managed Pi at this version ("" = none)
	npmFails        bool   // every Pi install attempt fails
	// npm exits 0 but lays down piPinRegistryLatest whatever it was asked for
	npmIgnoresVersion bool
	args              []string
}

type piPinResult struct {
	code        int
	combined    string
	npmCalls    [][]string // argv of every npm invocation
	sleeps      []string
	home        string
	packageJSON string
	seeded      string // the managed package.json the run started with
}

func runPiPinInstall(t *testing.T, o piPinOpts) piPinResult {
	t.Helper()

	root := t.TempDir()
	binDir := filepath.Join(root, "fakebin")
	home := filepath.Join(root, "home")
	tmp := filepath.Join(root, "tmp")
	for _, d := range []string{binDir, home, tmp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeExec := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeExec("curl", piPinCurlScript)
	writeExec("node", piPinNodeScript)
	writeExec("npm", piPinNpmScript)
	writeExec("sleep", piPinSleepScript)
	for _, name := range []string{
		"security", "sudo", "install", "update-ca-certificates", "update-ca-trust", "brew", "apt-get",
	} {
		writeExec(name, piPinRefuserScript)
	}

	piDir := filepath.Join(home, ".void-code", "runtime", "pi", "node_modules", "@earendil-works", "pi-coding-agent")
	packageJSON := filepath.Join(piDir, "package.json")
	seeded := ""
	if o.existingVersion != "" {
		if err := os.MkdirAll(filepath.Join(piDir, "dist"), 0o755); err != nil {
			t.Fatal(err)
		}
		manifest := `{
  "name": "@earendil-works/pi-coding-agent",
  "version": "` + o.existingVersion + `",
  "description": "coding agent",
  "type": "module",
  "bin": {
    "pi": "dist/cli.js"
  },
  "engines": {
    "node": ">=20.0.0"
  }
}
`
		if err := os.WriteFile(packageJSON, []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		seeded = manifest
		if err := os.WriteFile(filepath.Join(piDir, "dist", "cli.js"), []byte("#!/usr/bin/env node\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	npmLog := filepath.Join(root, "npm.log")
	sleepLog := filepath.Join(root, "sleep.log")
	for _, p := range []string{npmLog, sleepLog} {
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mode := "ok"
	if o.npmFails {
		mode = "fail"
	} else if o.npmIgnoresVersion {
		mode = "ignore-version"
	}

	// Built from scratch, not from os.Environ(): the real HOME must not be able
	// to reach the script even by accident.
	env := []string{
		"PATH=" + binDir + ":/usr/bin:/bin:/usr/sbin:/sbin",
		"HOME=" + home,
		"TMPDIR=" + tmp,
		"SHELL=/bin/zsh",
		"VC_AUTH_HOST=http://127.0.0.1:1",
		"VC_SKIP_DOWNLOAD=1",
		"VC_INSTALL_YES=1",
		"VC_INSTALL_PI=1",
		"VC_INSTALL_CLAUDE=0",
		"VC_INSTALL_CODEX=0",
		"PIPIN_NPM_LOG=" + npmLog,
		"PIPIN_NPM_MODE=" + mode,
		"PIPIN_SLEEP_LOG=" + sleepLog,
		"PIPIN_REGISTRY_LATEST=" + piPinRegistryLatest,
	}

	cmd := exec.Command("sh", append([]string{"install.sh"}, o.args...)...)
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("НЕ СМОГ: could not run install.sh: %v\n%s", err, output)
		}
		code = exitErr.ExitCode()
	}

	var calls [][]string
	argRe := regexp.MustCompile(`\[([^\]]*)\]`)
	for _, line := range strings.Split(piPinReadFile(t, npmLog), "\n") {
		if !strings.HasPrefix(line, "NPM") {
			continue
		}
		var argv []string
		for _, m := range argRe.FindAllStringSubmatch(line, -1) {
			argv = append(argv, m[1])
		}
		calls = append(calls, argv)
	}
	var sleeps []string
	for _, line := range strings.Split(piPinReadFile(t, sleepLog), "\n") {
		if strings.TrimSpace(line) != "" {
			sleeps = append(sleeps, line)
		}
	}

	return piPinResult{
		code:        code,
		combined:    string(output),
		npmCalls:    calls,
		sleeps:      sleeps,
		home:        home,
		packageJSON: packageJSON,
		seeded:      seeded,
	}
}

func piPinReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// piInstallCalls are the npm invocations that install Pi.
func piInstallCalls(calls [][]string) [][]string {
	var out [][]string
	for _, argv := range calls {
		install, pi := false, false
		for _, a := range argv {
			switch {
			case a == "install" || a == "i" || a == "add":
				install = true
			case strings.HasPrefix(a, piPinPackage):
				pi = true
			}
		}
		if install && pi {
			out = append(out, argv)
		}
	}
	return out
}

func formatCalls(calls [][]string) string {
	if len(calls) == 0 {
		return "(npm was not invoked)"
	}
	var lines []string
	for _, argv := range calls {
		lines = append(lines, "npm "+strings.Join(argv, " "))
	}
	return strings.Join(lines, "\n")
}

// requirePinnedPiInstall: every Pi install passes exactly the pinned spec as an
// argument, and there is at least one.
func requirePinnedPiInstall(t *testing.T, r piPinResult, pin, requirement string) {
	t.Helper()
	calls := piInstallCalls(r.npmCalls)
	if len(calls) == 0 {
		t.Fatalf("%s: npm was never asked to install %s into the managed runtime\nnpm calls:\n%s\noutput:\n%s",
			requirement, piPinSpec(pin), formatCalls(r.npmCalls), r.combined)
	}
	for _, argv := range calls {
		ok := false
		for _, a := range argv {
			if a == piPinSpec(pin) {
				ok = true
			} else if strings.HasPrefix(a, piPinPackage) {
				ok = false
				break
			}
		}
		if !ok {
			t.Errorf("%s: npm installed Pi without the version pinned in desktop/runtime/pi/package.json — want the argument %q\ngot: npm %s",
				requirement, piPinSpec(pin), strings.Join(argv, " "))
		}
	}
}

// requireNoGlobalPiInstall: Pi belongs only in the managed runtime, so npm is
// never asked to install it globally (-g / --global), at any version.
func requireNoGlobalPiInstall(t *testing.T, r piPinResult, requirement string) {
	t.Helper()
	for _, argv := range piInstallCalls(r.npmCalls) {
		for _, a := range argv {
			if a == "-g" || a == "--global" || strings.HasPrefix(a, "--global=") {
				t.Errorf("%s: npm installed Pi globally; Pi belongs only in the managed runtime\ngot: npm %s",
					requirement, strings.Join(argv, " "))
				break
			}
		}
	}
}

func installedPiVersion(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("read %s: %v", path, err)
	}
	var m struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("managed Pi package.json %s is not JSON: %v\n%s", path, err, data)
	}
	return m.Version
}

// ── install.sh: behaviour ────────────────────────────────────────────────────

// Requirement 1: a fresh machine gets exactly the pinned Pi.
func TestShellInstallerPiPinFreshInstall(t *testing.T) {
	skipInstallShOnWindows(t)

	if testing.Short() {
		t.Skip("runs the shell installer with command fixtures")
	}
	pin := piPinFromDesktop(t)

	r := runPiPinInstall(t, piPinOpts{})
	if r.code != 0 {
		t.Fatalf("installer exited %d\n%s", r.code, r.combined)
	}
	requirePinnedPiInstall(t, r, pin, "requirement 1 (install the pinned Pi)")
	requireNoGlobalPiInstall(t, r, "requirement 1 (install Pi only into the managed runtime)")
	if got := installedPiVersion(t, r.packageJSON); got != pin {
		t.Errorf("requirement 1 (install the pinned Pi): the managed runtime ended up with Pi %q, want the desktop pin %q",
			got, pin)
	}
}

// Requirement 3: a managed Pi at a different version is replaced by the pin; a
// managed Pi already at the pin is left alone.
func TestShellInstallerPiPinExistingManagedRuntime(t *testing.T) {
	skipInstallShOnWindows(t)

	if testing.Short() {
		t.Skip("runs the shell installer with command fixtures")
	}
	pin := piPinFromDesktop(t)

	t.Run("a different version is reinstalled at the pin", func(t *testing.T) {
		const stale = "0.84.1"
		if stale == pin {
			t.Fatalf("НЕ СМОГ: the stale fixture version %q equals the pin; pick another", stale)
		}
		r := runPiPinInstall(t, piPinOpts{existingVersion: stale})
		if r.code != 0 {
			t.Fatalf("installer exited %d\n%s", r.code, r.combined)
		}
		if len(piInstallCalls(r.npmCalls)) == 0 {
			t.Fatalf("requirement 3 (reinstall a managed Pi at another version): the managed runtime held Pi %s, the pin is %s, and the installer did not reinstall it\nnpm calls:\n%s\noutput:\n%s",
				stale, pin, formatCalls(r.npmCalls), r.combined)
		}
		requirePinnedPiInstall(t, r, pin, "requirement 3 (reinstall a managed Pi at another version)")
		requireNoGlobalPiInstall(t, r, "requirement 3 (reinstall Pi only into the managed runtime)")
		if got := installedPiVersion(t, r.packageJSON); got != pin {
			t.Errorf("requirement 3 (reinstall a managed Pi at another version): the managed runtime still holds Pi %q after the run, want %q",
				got, pin)
		}
	})

	t.Run("the pinned version is left alone", func(t *testing.T) {
		r := runPiPinInstall(t, piPinOpts{existingVersion: pin})
		if r.code != 0 {
			t.Fatalf("installer exited %d\n%s", r.code, r.combined)
		}
		if calls := piInstallCalls(r.npmCalls); len(calls) != 0 {
			t.Errorf("requirement 3 (do not reinstall a managed Pi already at the pin): the runtime already held Pi %s and npm was asked to install it again:\n%s",
				pin, formatCalls(calls))
		}
		if got := piPinReadFile(t, r.packageJSON); got != r.seeded {
			t.Errorf("requirement 3 (do not reinstall a managed Pi already at the pin): the managed package.json was rewritten\nbefore:\n%s\nafter:\n%s",
				r.seeded, got)
		}
	})
}

// Requirement 1, checked on the result rather than on npm's exit code: npm
// exits 0 but the managed runtime ends up holding a Pi other than the pin (a
// registry or cache that answered with something else). That Pi is not the one
// the vc extension supports, so the installer must not report it installed.
// It must take the path it takes today when a managed Pi install fails: "npm
// install failed." followed by a "Run manually:" line naming the pinned spec.
// (NEXT STEPS "Install Pi" is the other failure signal, but it is skipped under
// VC_SKIP_DOWNLOAD=1, which this harness needs.)
func TestShellInstallerPiPinVerifiesInstalledVersion(t *testing.T) {
	skipInstallShOnWindows(t)

	if testing.Short() {
		t.Skip("runs the shell installer with command fixtures")
	}
	pin := piPinFromDesktop(t)
	if pin == piPinRegistryLatest {
		t.Fatalf("НЕ СМОГ: the registry-latest fixture %q equals the pin; pick another", piPinRegistryLatest)
	}
	successRe := regexp.MustCompile(regexp.QuoteMeta(piPinPackage) + `(@\S*)? installed`)
	alreadyRe := regexp.MustCompile(`(?i)\bpi already installed`)

	for _, tc := range []struct {
		name     string
		existing string
	}{
		{"fresh runtime", ""},
		{"managed Pi at another version", "0.84.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := runPiPinInstall(t, piPinOpts{existingVersion: tc.existing, npmIgnoresVersion: true})
			if r.code != 0 {
				t.Fatalf("installer exited %d\n%s", r.code, r.combined)
			}
			if len(piInstallCalls(r.npmCalls)) == 0 {
				t.Fatalf("requirement 1/3: npm was never asked to install Pi (the managed runtime held %q, the pin is %s), so there was no result to verify\n%s",
					tc.existing, pin, r.combined)
			}
			if got := installedPiVersion(t, r.packageJSON); got != piPinRegistryLatest {
				t.Fatalf("НЕ СМОГ: the npm fixture was meant to leave Pi %q in the managed runtime, found %q", piPinRegistryLatest, got)
			}

			for _, line := range strings.Split(r.combined, "\n") {
				if successRe.MatchString(line) || alreadyRe.MatchString(line) {
					t.Errorf("requirement 1 (verify the installed Pi is the pin): npm exited 0 but left Pi %s in the managed runtime (pin %s), and the installer reported success:\n%s",
						piPinRegistryLatest, pin, strings.TrimSpace(line))
				}
			}
			if !strings.Contains(r.combined, "npm install failed") {
				t.Errorf("requirement 1 (verify the installed Pi is the pin): a managed Pi at %s instead of %s must be treated as a failed install (\"npm install failed.\"), got:\n%s",
					piPinRegistryLatest, pin, r.combined)
			}
			var manual []string
			for _, line := range strings.Split(r.combined, "\n") {
				if strings.Contains(line, "Run manually") {
					manual = append(manual, line)
				}
			}
			if len(manual) == 0 {
				t.Errorf("requirement 1 (verify the installed Pi is the pin): the failed install printed no \"Run manually\" line")
			}
			for _, line := range manual {
				if problems, pinned := piPinMentionProblems(line, pin); len(problems) > 0 || pinned == 0 {
					t.Errorf("requirement 2 (printed commands name the pin): the manual command after a wrong-version install does not name %q:\n%s",
						piPinSpec(pin), line)
				}
			}
		})
	}
}

// Requirement 2, the printed commands the shell installer can be driven to
// print without a terminal: the dry-run plan and the "Run manually:" line after
// npm gives up.
func TestShellInstallerPiPinPrintedCommands(t *testing.T) {
	skipInstallShOnWindows(t)

	if testing.Short() {
		t.Skip("runs the shell installer with command fixtures")
	}
	pin := piPinFromDesktop(t)

	t.Run("dry-run WOULD line", func(t *testing.T) {
		r := runPiPinInstall(t, piPinOpts{args: []string{"--dry-run"}})
		if r.code != 0 {
			t.Fatalf("dry-run exited %d\n%s", r.code, r.combined)
		}
		if len(r.npmCalls) != 0 {
			t.Errorf("dry-run invoked npm:\n%s", formatCalls(r.npmCalls))
		}
		var would []string
		for _, line := range strings.Split(r.combined, "\n") {
			if strings.HasPrefix(line, "WOULD:") && strings.Contains(line, "npm") && strings.Contains(line, piPinPackage) {
				would = append(would, line)
			}
		}
		if len(would) == 0 {
			t.Fatalf("requirement 2 (printed commands name the pin): dry-run printed no WOULD: npm line for Pi\n%s", r.combined)
		}
		for _, line := range would {
			if problems, _ := piPinMentionProblems(line, pin); len(problems) > 0 {
				t.Errorf("requirement 2 (printed commands name the pin): the dry-run plan installs Pi without %q:\n%s",
					"@"+pin, line)
			}
		}
	})

	t.Run("Run manually line after npm fails", func(t *testing.T) {
		r := runPiPinInstall(t, piPinOpts{npmFails: true})
		if r.code != 0 {
			t.Fatalf("installer exited %d\n%s", r.code, r.combined)
		}
		if len(piInstallCalls(r.npmCalls)) == 0 {
			t.Fatalf("НЕ СМОГ: npm was never asked to install Pi, so the failure path was not reached\n%s", r.combined)
		}
		var manual []string
		for _, line := range strings.Split(r.combined, "\n") {
			if strings.Contains(line, "Run manually") {
				manual = append(manual, line)
			}
		}
		if len(manual) == 0 {
			t.Fatalf("НЕ СМОГ: npm failed every attempt and the installer printed no \"Run manually\" line\n%s", r.combined)
		}
		for _, line := range manual {
			problems, pinned := piPinMentionProblems(line, pin)
			if len(problems) > 0 || pinned == 0 {
				t.Errorf("requirement 2 (printed commands name the pin): the manual command printed after a failed install does not name %q:\n%s",
					piPinSpec(pin), line)
			}
		}
	})
}

// ── install.sh: every Pi npm command site, statically ────────────────────────
//
// Two print sites cannot be reached from a test without a terminal or a full
// download: "Run when ready:" (needs `[ -t 0 ]` and an answer on /dev/tty) and
// the NEXT STEPS block (skipped under VC_SKIP_DOWNLOAD=1). They are checked on
// the script text instead, with the installer's own variables expanded, so a
// pin kept in a variable ($PI_VERSION, $PI_SPEC, …) counts exactly like one
// written inline.
//
// A site is any non-comment line that, once rendered, is an `npm … install`
// command and either names the Pi package or installs into the managed Pi
// runtime (…/runtime/pi). Every such line must name the pinned spec and no
// other spelling of the package. Blind spots, named rather than hidden: a
// package passed through a function's positional parameter ("$1") cannot be
// resolved from the text — at a Pi site that reads as "no pin" (red), and in a
// generic printer that installs somewhere else it is not a Pi site at all.
func TestShellInstallerPiPinEveryNpmCommandSite(t *testing.T) {
	pin := piPinFromDesktop(t)
	src := readInstaller(t, "install.sh")
	vars := shAssignments(src)

	type site struct {
		line     int
		source   string
		rendered string
	}
	var sites []site
	for i, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		rendered := shRenderLine(line, vars)
		if !shNpmInstallRe.MatchString(rendered) {
			continue
		}
		if !strings.Contains(rendered, piPinPackage) && !strings.Contains(rendered, "/runtime/pi") {
			continue
		}
		sites = append(sites, site{i + 1, line, rendered})
	}
	if len(sites) == 0 {
		t.Fatalf("НЕ СМОГ: found no npm install command for Pi in install.sh; the reader no longer recognises how it is written")
	}
	for _, s := range sites {
		problems, pinned := piPinMentionProblems(s.rendered, pin)
		if len(problems) > 0 || pinned == 0 {
			t.Errorf("requirement 2 (every Pi npm command names the pin): install.sh:%d does not name %q\n    source:   %s\n    rendered: %s",
				s.line, piPinSpec(pin), s.source, s.rendered)
		}
	}
}

var (
	shNpmInstallRe = regexp.MustCompile(`(^|[^A-Za-z0-9_])npm\s[^|;&]*\binstall\b`)
	shAssignRe     = regexp.MustCompile(`^(?:export\s+|local\s+|readonly\s+)?([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)
	shVarRe        = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::?[-=+?]([^}]*))?\}|\$([A-Za-z_][A-Za-z0-9_]*)`)
)

// shAssignments collects NAME=value assignments from install.sh, first one
// wins (the top-level defaults come first in the file).
func shAssignments(src string) map[string]string {
	vars := map[string]string{}
	for _, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "#") {
			continue
		}
		m := shAssignRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if _, seen := vars[m[1]]; seen {
			continue
		}
		words := shWords(m[2])
		if len(words) == 0 {
			vars[m[1]] = ""
			continue
		}
		vars[m[1]] = words[0].text
		if words[0].single {
			vars[m[1]] = "\x00" + words[0].text // literal, never expanded
		}
	}
	return vars
}

func shExpand(s string, vars map[string]string, depth int) string {
	if depth > 12 {
		return s
	}
	return shVarRe.ReplaceAllStringFunc(s, func(ref string) string {
		m := shVarRe.FindStringSubmatch(ref)
		name, def := m[1], m[2]
		if name == "" {
			name = m[3]
		}
		v, ok := vars[name]
		if !ok {
			if m[1] != "" && def != "" {
				return shExpand(def, vars, depth+1)
			}
			return ref
		}
		if strings.HasPrefix(v, "\x00") {
			return v[1:]
		}
		return shExpand(v, vars, depth+1)
	})
}

type shWord struct {
	text   string
	single bool // entirely single-quoted: no expansion
	op     bool // a redirection or control operator
}

// shWords splits a line into shell words, well enough for printf lines and
// assignment values: quotes are honoured, redirections and operators end up as
// words of their own.
func shWords(s string) []shWord {
	var out []shWord
	i := 0
	for i < len(s) {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= len(s) {
			break
		}
		if s[i] == '#' {
			break
		}
		if strings.ContainsRune(";&|<>", rune(s[i])) || (s[i] >= '0' && s[i] <= '9' && i+1 < len(s) && s[i+1] == '>') {
			j := i
			for j < len(s) && !strings.ContainsRune(" \t'\"", rune(s[j])) {
				j++
			}
			out = append(out, shWord{text: s[i:j], op: true})
			i = j
			continue
		}
		var b strings.Builder
		single := true
		quotedAny := false
		for i < len(s) && s[i] != ' ' && s[i] != '\t' && !strings.ContainsRune(";&|<>", rune(s[i])) {
			switch s[i] {
			case '\'':
				j := strings.IndexByte(s[i+1:], '\'')
				if j < 0 {
					j = len(s) - i - 1
				}
				b.WriteString(s[i+1 : i+1+j])
				i += j + 2
				quotedAny = true
			case '"':
				j := i + 1
				for j < len(s) && s[j] != '"' {
					if s[j] == '\\' {
						j++
					}
					j++
				}
				if j > len(s) {
					j = len(s)
				}
				b.WriteString(s[i+1 : j])
				i = j + 1
				single = false
				quotedAny = true
			default:
				b.WriteByte(s[i])
				i++
				single = false
			}
		}
		out = append(out, shWord{text: b.String(), single: single && quotedAny})
	}
	return out
}

// shRenderLine renders a printf line as it would print (%s filled in order),
// and any other line with its variables expanded.
func shRenderLine(line string, vars map[string]string) string {
	words := shWords(line)
	if len(words) >= 2 && words[0].text == "printf" {
		format := words[1].text
		if !words[1].single {
			format = shExpand(format, vars, 0)
		}
		var args []string
		for _, w := range words[2:] {
			if w.op {
				break
			}
			if w.single {
				args = append(args, w.text)
			} else {
				args = append(args, shExpand(w.text, vars, 0))
			}
		}
		var b strings.Builder
		n := 0
		for i := 0; i < len(format); i++ {
			if format[i] == '%' && i+1 < len(format) {
				switch format[i+1] {
				case '%':
					b.WriteByte('%')
					i++
					continue
				case 's', 'd':
					if n < len(args) {
						b.WriteString(args[n])
					}
					n++
					i++
					continue
				}
			}
			b.WriteByte(format[i])
		}
		return b.String()
	}
	return shExpand(line, vars, 0)
}

// ── install.ps1 ──────────────────────────────────────────────────────────────

var (
	psAssignRe = regexp.MustCompile(`^\$(?:script:|global:)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.+)$`)
	psVarRe    = regexp.MustCompile(`\$\(\s*\$([A-Za-z_][A-Za-z0-9_]*)\s*\)|\$\{([A-Za-z_][A-Za-z0-9_]*)\}|\$(?:script:|global:)?([A-Za-z_][A-Za-z0-9_]*)`)
	psFuncRe   = regexp.MustCompile(`(?mi)^\s*function\s+([A-Za-z][A-Za-z0-9-]*)\s*\{`)
	// the managed Pi's own manifest, spelled with either separator
	psPiManifestRe = regexp.MustCompile(`pi-coding-agent['"]?\s*[\\/,]\s*['"]?package\.json`)
)

// psAssignments collects `$Name = 'literal'` / `$Name = "string"` assignments,
// keyed case-insensitively as PowerShell does. Values that are not a plain
// string literal (Join-Path …, arrays, calls) are kept raw: psClosure still
// searches them for the pin and the manifest path, psExpand never inlines them.
func psAssignments(src string) map[string]string {
	vars := map[string]string{}
	for _, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "#") {
			continue
		}
		m := psAssignRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := strings.ToLower(m[1])
		if _, seen := vars[name]; seen {
			continue
		}
		v := strings.TrimSpace(m[2])
		switch {
		case len(v) >= 2 && v[0] == '\'' && strings.LastIndexByte(v, '\'') > 0:
			vars[name] = "\x00" + v[1:strings.LastIndexByte(v, '\'')]
		case len(v) >= 2 && v[0] == '"' && strings.LastIndexByte(v, '"') > 0:
			vars[name] = v[1:strings.LastIndexByte(v, '"')]
		default:
			vars[name] = "\x01" + v // an expression: searched, never expanded
		}
	}
	return vars
}

func psExpand(s string, vars map[string]string, depth int) string {
	if depth > 12 {
		return s
	}
	return psVarRe.ReplaceAllStringFunc(s, func(ref string) string {
		m := psVarRe.FindStringSubmatch(ref)
		name := m[1] + m[2] + m[3]
		v, ok := vars[strings.ToLower(name)]
		if !ok || strings.HasPrefix(v, "\x01") {
			return ref
		}
		if strings.HasPrefix(v, "\x00") {
			return v[1:]
		}
		return psExpand(v, vars, depth+1)
	})
}

// psFunctions returns every `function Name { … }` body by brace matching.
func psFunctions(src string) map[string]string {
	funcs := map[string]string{}
	for _, loc := range psFuncRe.FindAllStringSubmatchIndex(src, -1) {
		name := src[loc[2]:loc[3]]
		open := loc[1] - 1
		depth := 0
		end := len(src)
		for i := open; i < len(src); i++ {
			if src[i] == '{' {
				depth++
			} else if src[i] == '}' {
				depth--
				if depth == 0 {
					end = i + 1
					break
				}
			}
		}
		funcs[name] = src[open:end]
	}
	return funcs
}

// psClosure is seed plus the bodies of every function it names and the
// assignments of every variable it names, transitively.
func psClosure(seed string, funcs map[string]string, vars map[string]string) string {
	var b strings.Builder
	b.WriteString(seed)
	seenF := map[string]bool{}
	seenV := map[string]bool{}
	queue := []string{seed}
	for len(queue) > 0 {
		text := queue[0]
		queue = queue[1:]
		names := make([]string, 0, len(funcs))
		for n := range funcs {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			if seenF[n] {
				continue
			}
			if regexp.MustCompile(`(?i)(^|[^A-Za-z0-9-])` + regexp.QuoteMeta(n) + `($|[^A-Za-z0-9-])`).MatchString(text) {
				seenF[n] = true
				b.WriteString("\n" + funcs[n])
				queue = append(queue, funcs[n])
			}
		}
		for _, m := range psVarRe.FindAllStringSubmatch(text, -1) {
			n := strings.ToLower(m[1] + m[2] + m[3])
			v, ok := vars[n]
			if !ok || seenV[n] {
				continue
			}
			seenV[n] = true
			v = strings.TrimPrefix(strings.TrimPrefix(v, "\x00"), "\x01")
			b.WriteString("\n" + v)
			queue = append(queue, v)
		}
	}
	return b.String()
}

// psIsLiteralAssignment: `$Name = 'text'` or `$Name = "text"` and nothing
// else. `$ok = Install-NpmAgent -Package '…'` is a call, not a definition.
func psIsLiteralAssignment(line string) bool {
	m := psAssignRe.FindStringSubmatch(line)
	if m == nil {
		return false
	}
	v := strings.TrimSpace(m[2])
	if len(v) < 2 {
		return false
	}
	q := v[0]
	if q != '\'' && q != '"' {
		return false
	}
	return strings.IndexByte(v[1:], q) == len(v)-2
}

// Requirements 1 and 2 for install.ps1: every mention of the Pi package in code
// — the managed install at the -Package call site, the dry-run WOULD: line, the
// "install manually" / NEXT STEPS lines — is spelled with the pin, directly or
// through a variable that resolves to it. Assignments are not mentions (a
// `$PiPackageName = '@earendil-works/pi-coding-agent'` is fine); their uses are.
func TestPowerShellInstallerPiPinEveryPackageMention(t *testing.T) {
	pin := piPinFromDesktop(t)
	src := readInstaller(t, "install.ps1")
	vars := psAssignments(src)

	mentions := 0
	for i, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if psIsLiteralAssignment(line) {
			continue
		}
		rendered := psExpand(line, vars, 0)
		problems, pinned := piPinMentionProblems(rendered, pin)
		mentions += pinned + len(problems)
		for _, p := range problems {
			t.Errorf("requirement 1/2 (install.ps1 installs and prints the pinned Pi): install.ps1:%d names %s without %q\n    source:   %s\n    at:       …%s…",
				i+1, piPinPackage, "@"+pin, line, p)
		}
	}
	if mentions == 0 {
		t.Fatalf("НЕ СМОГ: install.ps1 no longer names %s anywhere in code; the reader cannot find the Pi install", piPinPackage)
	}
}

// Requirement 3 for install.ps1: the decision that skips the Pi install as
// "already installed" must look at the managed Pi's version, not only at
// whether its entrypoint exists. Read statically: the Pi health path (the body
// of Test-AgentHealthy and Install-NpmAgent up to its "already installed"
// message, followed through every function and variable they name) must both
// read the managed runtime's pi-coding-agent/package.json and involve the pin.
func TestPowerShellInstallerPiPinHealthComparesVersion(t *testing.T) {
	pin := piPinFromDesktop(t)
	src := readInstaller(t, "install.ps1")
	vars := psAssignments(src)
	funcs := psFunctions(src)

	health, ok := funcs["Test-AgentHealthy"]
	if !ok {
		t.Fatalf("НЕ СМОГ: install.ps1 has no function Test-AgentHealthy; the reader cannot find the Pi health decision")
	}
	install, ok := funcs["Install-NpmAgent"]
	if !ok {
		t.Fatalf("НЕ СМОГ: install.ps1 has no function Install-NpmAgent")
	}
	if k := strings.Index(install, "already installed"); k >= 0 {
		install = install[:k]
	}

	closure := psExpand(psClosure(health+"\n"+install, funcs, vars), vars, 0)
	if !psPiManifestRe.MatchString(closure) {
		t.Errorf("requirement 3 (install.ps1 reinstalls a managed Pi at another version): the \"already installed\" decision never reads the managed runtime's pi-coding-agent\\package.json — a Pi at any version passes as healthy")
	}
	if !strings.Contains(closure, pin) {
		t.Errorf("requirement 3 (install.ps1 reinstalls a managed Pi at another version): the \"already installed\" decision never compares against the pin %q", pin)
	}
}

// Requirement 2 for install.ps1, run for real where PowerShell exists: the
// dry-run plan names the pinned Pi. The dry run exits before any write, so it
// is safe on Windows too.
func TestPowerShellInstallerPiPinDryRun(t *testing.T) {
	pin := piPinFromDesktop(t)
	ps := ""
	if p, err := exec.LookPath("pwsh"); err == nil {
		ps = p
	} else if p, err := exec.LookPath("powershell"); err == nil {
		ps = p
	}
	if ps == "" {
		t.Skip("НЕ СМОГ: PowerShell is not installed, so install.ps1 cannot be run here")
	}

	home := t.TempDir()
	cmd := exec.Command(ps, "-NoProfile", "-File", "install.ps1")
	cmd.Env = append(os.Environ(),
		"VC_INSTALL_DRY_RUN=1",
		"VC_INSTALL_PI=1",
		"VC_INSTALL_CLAUDE=0",
		"VC_INSTALL_CODEX=0",
		"USERPROFILE="+home,
		"HOME="+home,
		"POWERSHELL_TELEMETRY_OPTOUT=1",
		"POWERSHELL_UPDATECHECK=Off",
	)
	if runtime.GOOS != "windows" {
		cmd.Env = append(cmd.Env, "APPDATA="+filepath.Join(home, "AppData", "Roaming"))
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("PowerShell dry-run failed: %v\n%s", err, output)
	}
	out := winANSI.ReplaceAllString(string(output), "")

	var would []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "WOULD:") && strings.Contains(line, piPinPackage) {
			would = append(would, strings.TrimSpace(line))
		}
	}
	if len(would) == 0 {
		t.Fatalf("requirement 2 (printed commands name the pin): the PowerShell dry-run printed no WOULD: line for Pi\n%s", out)
	}
	for _, line := range would {
		if problems, pinned := piPinMentionProblems(line, pin); len(problems) > 0 || pinned == 0 {
			t.Errorf("requirement 2 (printed commands name the pin): the PowerShell dry-run plan installs Pi without %q:\n%s",
				"@"+pin, line)
		}
	}
}

// ── install.ps1: behaviour, run for real where PowerShell exists ─────────────
//
// The static checks above read install.ps1's text, and text survives mutations
// that behaviour does not: a version comparison inverted (-ne for -eq), or one
// that compares something meaningless, still mentions the manifest and the pin.
// These runs execute install.ps1 end to end the way installer_windows_download_test.go
// does — a local httptest host behind VC_AUTH_HOST serves version.json, the
// binary and the relay CA — but with Pi selected, and with `node` and `npm` on
// PATH as the same fixtures the shell harness uses. Skipped on Windows for the
// reason given there (install.ps1 writes the real HKCU PATH), and skipped where
// no PowerShell is installed.

type piPinPSOpts struct {
	existingVersion string // pre-create a managed Pi at this version ("" = none)
}

func runPiPinPowerShellInstall(t *testing.T, o piPinPSOpts) piPinResult {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("running install.ps1 on Windows would write the real HKCU PATH; USERPROFILE cannot redirect that")
	}
	ps := winPowerShell(t)

	root := t.TempDir()
	fakeBin := filepath.Join(root, "fakebin")
	home := filepath.Join(root, "home")
	appData := filepath.Join(root, "appdata")
	programFiles := filepath.Join(root, "programfiles")
	tmp := filepath.Join(root, "tmp")
	for _, d := range []string{fakeBin, home, appData, programFiles, tmp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{"node": piPinNodeScript, "npm": piPinNpmScript} {
		if err := os.WriteFile(filepath.Join(fakeBin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	runtimeDir := filepath.Join(home, ".void-code", "runtime", "pi")
	piDir := filepath.Join(runtimeDir, "node_modules", "@earendil-works", "pi-coding-agent")
	packageJSON := filepath.Join(piDir, "package.json")
	seeded := ""
	if o.existingVersion != "" {
		for _, d := range []string{filepath.Join(piDir, "dist"), filepath.Join(runtimeDir, "node_modules", ".bin")} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		seeded = `{
  "name": "@earendil-works/pi-coding-agent",
  "version": "` + o.existingVersion + `",
  "description": "coding agent",
  "type": "module",
  "bin": {
    "pi": "dist/cli.js"
  }
}
`
		for path, body := range map[string]string{
			packageJSON:                            seeded,
			filepath.Join(piDir, "dist", "cli.js"): "#!/usr/bin/env node\n",
			filepath.Join(runtimeDir, "node_modules", ".bin", "pi.cmd"): "@echo off\n",
		} {
			if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}

	npmLog := filepath.Join(root, "npm.log")
	if err := os.WriteFile(npmLog, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/vc/version.json":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"version":%q,"tag":"v%s","artifacts":{"windows/amd64":%q}}`,
				winFixtureVersion, winFixtureVersion, winArtifactPath)
		case "/vc/" + winArtifactPath:
			_, _ = w.Write([]byte(winPrimaryBytes))
		case "/vc/relay-ca.pem":
			fmt.Fprint(w, "-----BEGIN CERTIFICATE-----\nZmFrZQ==\n-----END CERTIFICATE-----\n")
		default:
			// SHA256SUMS included: a missing list is reported and the install
			// carries on, exactly as in the download suite's default.
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// Built from scratch, as in runWindowsInstall. ProgramFiles points at an
	// empty directory so Resolve-NpmCommand's explicit nodejs\npm.cmd probe
	// misses and it falls through to `npm` on PATH — the fixture.
	env := []string{
		"PATH=" + fakeBin + ":" + filepath.Dir(ps) + ":/usr/bin:/bin",
		"HOME=" + home,
		"USERPROFILE=" + home,
		"APPDATA=" + appData,
		"ProgramFiles=" + programFiles,
		"TMPDIR=" + tmp,
		"TEMP=" + tmp,
		"TMP=" + tmp,
		"POWERSHELL_TELEMETRY_OPTOUT=1",
		"POWERSHELL_UPDATECHECK=Off",
		"DOTNET_CLI_TELEMETRY_OPTOUT=1",
		"VC_AUTH_HOST=" + srv.URL,
		"VC_LANG=en",
		"VC_INSTALL_PI=1",
		"VC_INSTALL_CLAUDE=0",
		"VC_INSTALL_CODEX=0",
		"PIPIN_NPM_LOG=" + npmLog,
		"PIPIN_NPM_MODE=ok",
		"PIPIN_REGISTRY_LATEST=" + piPinRegistryLatest,
	}

	cmd := exec.Command(ps, "-NoProfile", "-File", "install.ps1")
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("НЕ СМОГ: could not run install.ps1: %v\n%s", err, output)
		}
		code = exitErr.ExitCode()
	}

	var calls [][]string
	argRe := regexp.MustCompile(`\[([^\]]*)\]`)
	for _, line := range strings.Split(piPinReadFile(t, npmLog), "\n") {
		if !strings.HasPrefix(line, "NPM") {
			continue
		}
		var argv []string
		for _, m := range argRe.FindAllStringSubmatch(line, -1) {
			argv = append(argv, m[1])
		}
		calls = append(calls, argv)
	}

	return piPinResult{
		code:        code,
		combined:    winANSI.ReplaceAllString(string(output), ""),
		npmCalls:    calls,
		home:        home,
		packageJSON: packageJSON,
		seeded:      seeded,
	}
}

// requirePiInstallIntoRuntime: every Pi install names the managed runtime as
// its --prefix (either spelling, either path separator).
func requirePiInstallIntoRuntime(t *testing.T, r piPinResult, requirement string) {
	t.Helper()
	want := filepath.Join(r.home, ".void-code", "runtime", "pi")
	for _, argv := range piInstallCalls(r.npmCalls) {
		prefix := ""
		for i, a := range argv {
			if a == "--prefix" && i+1 < len(argv) {
				prefix = argv[i+1]
			} else if strings.HasPrefix(a, "--prefix=") {
				prefix = strings.TrimPrefix(a, "--prefix=")
			}
		}
		if filepath.Clean(strings.ReplaceAll(prefix, `\`, "/")) != want {
			t.Errorf("%s: npm installed Pi with --prefix %q, want the managed runtime %q\ngot: npm %s",
				requirement, prefix, want, strings.Join(argv, " "))
		}
	}
}

// Requirements 1 and 3 for install.ps1, by behaviour.
func TestPowerShellInstallerPiPinBehaviour(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the PowerShell installer against a local fixture host")
	}
	pin := piPinFromDesktop(t)

	t.Run("managed Pi at the pin is left alone", func(t *testing.T) {
		r := runPiPinPowerShellInstall(t, piPinPSOpts{existingVersion: pin})
		if r.code != 0 {
			t.Fatalf("install.ps1 exited %d\n%s", r.code, r.combined)
		}
		if calls := piInstallCalls(r.npmCalls); len(calls) != 0 {
			t.Errorf("requirement 3 (do not reinstall a managed Pi already at the pin): the runtime already held Pi %s and install.ps1 asked npm to install it again:\n%s\noutput:\n%s",
				pin, formatCalls(calls), r.combined)
		}
		if got := piPinReadFile(t, r.packageJSON); got != r.seeded {
			t.Errorf("requirement 3 (do not reinstall a managed Pi already at the pin): the managed package.json was rewritten\nbefore:\n%s\nafter:\n%s",
				r.seeded, got)
		}
	})

	t.Run("managed Pi at another version is reinstalled at the pin", func(t *testing.T) {
		const stale = "0.84.1"
		if stale == pin {
			t.Fatalf("НЕ СМОГ: the stale fixture version %q equals the pin; pick another", stale)
		}
		r := runPiPinPowerShellInstall(t, piPinPSOpts{existingVersion: stale})
		if r.code != 0 {
			t.Fatalf("install.ps1 exited %d\n%s", r.code, r.combined)
		}
		if len(piInstallCalls(r.npmCalls)) == 0 {
			t.Fatalf("requirement 3 (reinstall a managed Pi at another version): the managed runtime held Pi %s, the pin is %s, and install.ps1 did not reinstall it\nnpm calls:\n%s\noutput:\n%s",
				stale, pin, formatCalls(r.npmCalls), r.combined)
		}
		requirePinnedPiInstall(t, r, pin, "requirement 3 (install.ps1 reinstalls a managed Pi at another version)")
		requirePiInstallIntoRuntime(t, r, "requirement 3 (install.ps1 reinstalls a managed Pi at another version)")
		if got := installedPiVersion(t, r.packageJSON); got != pin {
			t.Errorf("requirement 3 (install.ps1 reinstalls a managed Pi at another version): the managed runtime holds Pi %q after the run, want %q",
				got, pin)
		}
	})

	t.Run("fresh runtime gets the pin", func(t *testing.T) {
		r := runPiPinPowerShellInstall(t, piPinPSOpts{})
		if r.code != 0 {
			t.Fatalf("install.ps1 exited %d\n%s", r.code, r.combined)
		}
		requirePinnedPiInstall(t, r, pin, "requirement 1 (install.ps1 installs the pinned Pi)")
		requirePiInstallIntoRuntime(t, r, "requirement 1 (install.ps1 installs the pinned Pi)")
		if got := installedPiVersion(t, r.packageJSON); got != pin {
			t.Errorf("requirement 1 (install.ps1 installs the pinned Pi): the managed runtime ended up with Pi %q, want %q",
				got, pin)
		}
	})
}
