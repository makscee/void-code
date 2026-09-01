package installercontract

// install.sh: the GitHub-release mirror contract.
//
// `install.sh` in THIS repository is the source of truth for the installer;
// `.github/workflows/release.yml` copies it over `void-auth/public/vc/install.sh`
// on every stable release. The mirror fallback (public GitHub release + sha256)
// landed in the served copy only, so the first stable release overwrites the fix
// with this file. That is what these tests exist to stop: three subtests, one per
// element of the contract, so a missing element names itself.
//
//   1. a fallback source exists and is actually built (mirror serves the binary
//      when the primary host does not);
//   2. bytes from that source are sha256-checked BEFORE anything replaces `vc`;
//   3. downloads get whole-process retries, not one shot.
//
// Spec: docs/superpowers/specs/2026-08-31-installer-fallback-fixes.md §1 (and §2
// for the retry element). The sibling copy in void-auth carries the same contract
// in test/vc-install-fallback.test.ts — a different language and a wider set of
// cases; nothing here is derived from it by copying.
//
// Isolation contract — these tests NEVER touch the network or the real HOME:
//   - a fake `curl` is first on PATH and serves fixtures from a temp dir; it is
//     the only fetcher install.sh can find, and it logs every invocation;
//   - `node`, `security`, `sudo`, `install`, `brew`, `apt-get` and the CA-trust
//     tools are fake too, so no Node.js is installed, no keychain is written and
//     no anchor lands in the system trust store;
//   - the environment is built from scratch (no os.Environ() passthrough), with
//     HOME and TMPDIR inside the test's temp dir, so ~/.void-code, ~/.zshrc and
//     the login keychain of this machine are never candidates;
//   - home_isolation_test.go re-checks that from outside, for the whole package.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Fixture release coordinates. Deliberately not a real 0.2.x version, so no
// assertion can pass against something already on disk.
const (
	mirrorFixtureTag     = "v9.9.9"
	mirrorFixtureVersion = "9.9.9"
	// The tag GitHub's "latest release" lookup reports. Different on purpose:
	// any mirror URL carrying it was built from that lookup, never from the
	// version.json the primary host served.
	mirrorLatestTag = "v8.8.8"
	mirrorAuthHost  = "https://auth.test.invalid"
	mirrorRepo      = "makscee/void-code"
)

var (
	mirrorPrimaryBytes = "PRIMARY-" + strings.Repeat("p", 4096) + "\n"
	mirrorMirrorBytes  = "MIRROR-" + strings.Repeat("m", 4096) + "\n"
	// What a mid-stream abort dumps at the destination before dying. Its own
	// marker, so a leftover is identifiable wherever it turns up.
	mirrorPartialBytes = "PARTIAL-" + strings.Repeat("x", 1024) + "\n"
)

// mirrorAssetName is the release asset name for this machine, spelled exactly as
// install.sh's detect_os/detect_arch spell it.
func mirrorAssetName() string {
	goos := "linux"
	if runtime.GOOS == "darwin" {
		goos = "darwin"
	}
	arch := "amd64"
	if runtime.GOARCH == "arm64" {
		arch = "arm64"
	}
	return fmt.Sprintf("vc-%s-%s", goos, arch)
}

func mirrorSHA256(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// ── fake executables ─────────────────────────────────────────────────────────

// Fake curl. Never opens a socket; logs every invocation; serves fixtures by URL.
// Written with $(...) rather than backticks so it can live in a Go raw string.
const fakeCurlScript = `#!/bin/sh
printf '%s\n' "$*" >> "$FAKE_LOG"

url=""
out=""
want_out=0
show_help=0
for a in "$@"; do
  if [ "$want_out" = 1 ]; then out="$a"; want_out=0; continue; fi
  case "$a" in
    --retry-all-errors)
      # Old curl builds abort on the unknown option before doing any work.
      if [ "$FAKE_CURL_OLD" = "1" ]; then
        printf 'curl: option --retry-all-errors: is unknown\n' >&2
        exit 2
      fi
      ;;
    -o|--output) want_out=1 ;;
    --output=*) out=$(printf '%s' "$a" | sed 's/^--output=//') ;;
    -h|--help) show_help=1 ;;
    *://*) url="$a" ;;
  esac
done

if [ "$show_help" = 1 ]; then
  printf 'Usage: curl [options...] <url>\n'
  printf ' -f, --fail          Fail silently on HTTP errors\n'
  printf ' -o, --output <file> Write to file instead of stdout\n'
  printf '     --retry <num>   Retry request if transient problems occur\n'
  printf '     --retry-delay <seconds> Wait time between retries\n'
  printf '     --retry-max-time <seconds> Retry only within this period\n'
  if [ "$FAKE_CURL_OLD" != "1" ]; then
    printf '     --retry-all-errors Retry all errors (use with --retry)\n'
  fi
  exit 0
fi

serve() {
  if [ -n "$out" ]; then cat "$1" > "$out"; else cat "$1"; fi
  exit 0
}

# Mimic a mid-stream abort: bytes land on disk, exit code is non-zero.
die() {
  if [ -n "$out" ]; then head -c 700 "$FAKE_DIR/partial" > "$out"; fi
  printf 'curl: (92) HTTP/2 stream 0 was not closed cleanly: INTERNAL_ERROR\n' >&2
  exit 92
}

# Mimic curl --fail against a route that is simply not there: no bytes, no partial
# file, exit 22. Distinct from die() on purpose — "the file does not exist yet"
# is the whole transitional case, and a harness that spelled it as a torn stream
# would be testing a different thing.
notfound() {
  printf 'curl: (22) The requested URL returned error: 404\n' >&2
  exit 22
}

# Count one whole-process attempt in the counter file $1 and print the total.
bump() {
  _n=0
  [ -f "$1" ] && _n=$(cat "$1")
  _n=$(expr "$_n" + 1)
  printf '%s' "$_n" > "$1"
  printf '%s' "$_n"
}

case "$url" in
  */vc/version.json)
    # version.json gets the same three modes as the binary, and for the same
    # reason: it carries the release tag the mirror is addressed by, so whether
    # it survives a flapping host is a behaviour of its own, not a detail of
    # the binary download. Its attempts are counted in a separate file — a
    # shared counter would let one fetch's failures satisfy the other's budget.
    case "$FAKE_VERSION_JSON" in
      ok)
        serve "$FAKE_DIR/version.json"
        ;;
      flaky)
        n=$(bump "$FAKE_DIR/version-attempts")
        if [ "$n" -gt "$FAKE_VERSION_FLAKY_N" ]; then
          serve "$FAKE_DIR/version.json"
        else
          die
        fi
        ;;
      *)
        die
        ;;
    esac
    ;;
  *releases/latest*)
    [ "$FAKE_LATEST" = "ok" ] || die
    serve "$FAKE_DIR/latest-api.json"
    ;;
  */vc/relay-ca.pem)
    [ "$FAKE_CA" = "ok" ] || die
    serve "$FAKE_DIR/relay-ca.pem"
    ;;
  *releases/download/*SHA256SUMS*)
    case "$FAKE_MIRROR_SUMS" in
      missing)  notfound ;;
      fail)     die ;;
      mismatch) serve "$FAKE_DIR/sums-mirror-bad" ;;
      *)        serve "$FAKE_DIR/sums-mirror" ;;
    esac
    ;;
  */vc/SHA256SUMS)
    # The primary host's own checksum list, served next to version.json. The
    # default is "missing" and not "ok": today the route is a 404 on the real
    # host, and a harness whose silent default was a healthy file would let a
    # caller that never thought about this route pass as if it had.
    case "${FAKE_PRIMARY_SUMS:-missing}" in
      missing)  notfound ;;
      fail)     die ;;
      mismatch) serve "$FAKE_DIR/sums-primary-bad" ;;
      noentry)  serve "$FAKE_DIR/sums-primary-noentry" ;;
      *)        serve "$FAKE_DIR/sums-primary" ;;
    esac
    ;;
  *releases/download/*)
    [ "$FAKE_MIRROR" = "ok" ] || die
    serve "$FAKE_DIR/mirror-bin"
    ;;
  */vc/bin/*)
    case "$FAKE_PRIMARY" in
      ok)
        serve "$FAKE_DIR/primary-bin"
        ;;
      flaky)
        n=$(bump "$FAKE_DIR/attempts")
        if [ "$n" -gt "$FAKE_FLAKY_N" ]; then
          serve "$FAKE_DIR/primary-bin"
        else
          die
        fi
        ;;
      *)
        die
        ;;
    esac
    ;;
  *)
    printf 'curl: (6) Could not resolve host\n' >&2
    exit 6
    ;;
esac
`

// macOS mktemp with no template ignores TMPDIR entirely (it uses the confstr
// _CS_DARWIN_USER_TEMP_DIR). This shim keeps every temp file install.sh makes
// inside the run's private TMPDIR on both platforms.
const fakeMktempScript = `#!/bin/sh
want_dir=0
for a in "$@"; do
  case "$a" in
    -d) want_dir=1 ;;
  esac
done
n=0
while :; do
  n=$(expr "$n" + 1)
  f="$TMPDIR/tmp.$$.$n"
  [ -e "$f" ] || break
done
if [ "$want_dir" = 1 ]; then mkdir "$f"; else : > "$f"; chmod 600 "$f"; fi
printf '%s\n' "$f"
`

const fakeNodeScript = `#!/bin/sh
case "$1" in
  --version|-v) printf 'v22.11.0\n' ;;
  *) exit 0 ;;
esac
`

// Every privileged / system-mutating helper install.sh may reach for is stubbed
// to a logged no-op failure. install.sh treats all of them as non-fatal.
const fakeRefuserScript = `#!/bin/sh
printf 'REFUSED %s %s\n' "$0" "$*" >> "$FAKE_LOG"
exit 1
`

// ── harness ──────────────────────────────────────────────────────────────────

type mirrorOpts struct {
	primary       string // ok | fail | flaky
	mirror        string // ok | fail
	sums          string // mirror's SHA256SUMS: ok | missing | fail | mismatch
	primarySums   string // $AUTH_HOST/vc/SHA256SUMS: ok | missing | fail | mismatch | noentry
	ca            string // ok | fail
	versionJSON   string // ok | fail | flaky
	latest        string // ok | fail
	flakyN        int    // how many primary attempts die before one succeeds
	versionFlakyN int    // how many version.json attempts die before one succeeds
	oldCurl       bool   // curl build without --retry-all-errors
	existingVC    string // pre-create $HOME/.void-code/bin/vc with this content
	existingCA    string // pre-create $HOME/.void-code/relay-ca.pem with this content
	existingRC    string // pre-create $HOME/.zshrc with this content
	skipDownload  bool   // VC_SKIP_DOWNLOAD=1
}

type mirrorResult struct {
	code     int
	combined string
	log      []string
	home     string
	vcPath   string
	caPath   string
	rcPath   string
	// The run's private TMPDIR. Nothing but install.sh's own mktemp writes
	// here, so whatever is left in it after the run is a leftover of the run.
	tmpDir string
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func runMirrorInstall(t *testing.T, o mirrorOpts) mirrorResult {
	t.Helper()

	root := t.TempDir()
	binDir := filepath.Join(root, "fakebin")
	fixtures := filepath.Join(root, "fixtures")
	home := filepath.Join(root, "home")
	tmp := filepath.Join(root, "tmp")
	for _, d := range []string{binDir, fixtures, home, tmp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	logPath := filepath.Join(root, "curl.log")
	writeFixture := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(fixtures, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	asset := mirrorAssetName()
	writeFixture("version.json", fmt.Sprintf(`{
  "version": %q,
  "tag": %q,
  "artifacts": {
    "darwin/amd64": "bin/vc-darwin-amd64",
    "darwin/arm64": "bin/vc-darwin-arm64",
    "linux/amd64": "bin/vc-linux-amd64",
    "linux/arm64": "bin/vc-linux-arm64",
    "darwin-amd64": "bin/vc-darwin-amd64",
    "darwin-arm64": "bin/vc-darwin-arm64",
    "linux-amd64": "bin/vc-linux-amd64",
    "linux-arm64": "bin/vc-linux-arm64"
  }
}
`, mirrorFixtureVersion, mirrorFixtureTag))
	writeFixture("latest-api.json", fmt.Sprintf(`{
  "tag_name": %q,
  "draft": false,
  "prerelease": false,
  "html_url": "https://github.com/%s/releases/tag/%s"
}
`, mirrorLatestTag, mirrorRepo, mirrorLatestTag))
	writeFixture("relay-ca.pem", "-----BEGIN CERTIFICATE-----\nZmFrZQ==\n-----END CERTIFICATE-----\n")
	writeFixture("primary-bin", mirrorPrimaryBytes)
	writeFixture("mirror-bin", mirrorMirrorBytes)
	// A decoy entry first: the checker must match the asset name, not line 1.
	writeFixture("sums-mirror", fmt.Sprintf("%s  vc-somewhere-else\n%s  %s\n",
		mirrorSHA256("decoy"), mirrorSHA256(mirrorMirrorBytes), asset))
	writeFixture("sums-mirror-bad", fmt.Sprintf("%s  %s\n", strings.Repeat("0", 64), asset))
	// The primary host's list, spelled the way the release actually spells it:
	// `sha256sum vc-* version.json > SHA256SUMS` runs inside dist/, so the names
	// are bare basenames even though the bytes are served from /vc/bin/. A decoy
	// first, for the same reason as the mirror's: match the asset, not line 1.
	writeFixture("sums-primary", fmt.Sprintf("%s  vc-somewhere-else\n%s  %s\n%s  version.json\n",
		mirrorSHA256("decoy"), mirrorSHA256(mirrorPrimaryBytes), asset, mirrorSHA256("version.json")))
	writeFixture("sums-primary-bad", fmt.Sprintf("%s  %s\n", strings.Repeat("0", 64), asset))
	// A list that exists and simply does not name this asset — indistinguishable
	// from an older format, which is why the spec treats it as "nothing to check
	// against" rather than as a mismatch.
	writeFixture("sums-primary-noentry", fmt.Sprintf("%s  vc-somewhere-else\n%s  version.json\n",
		mirrorSHA256("decoy"), mirrorSHA256("version.json")))
	writeFixture("partial", mirrorPartialBytes)

	writeExec := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeExec("curl", fakeCurlScript)
	writeExec("mktemp", fakeMktempScript)
	writeExec("node", fakeNodeScript)
	for _, name := range []string{
		"security", "sudo", "install", "update-ca-certificates", "update-ca-trust",
		"brew", "apt-get", "npm",
	} {
		writeExec(name, fakeRefuserScript)
	}

	if o.existingVC != "" {
		if err := os.MkdirAll(filepath.Join(home, ".void-code", "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, ".void-code", "bin", "vc"), []byte(o.existingVC), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if o.existingCA != "" {
		if err := os.MkdirAll(filepath.Join(home, ".void-code"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, ".void-code", "relay-ca.pem"), []byte(o.existingCA), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if o.existingRC != "" {
		if err := os.WriteFile(filepath.Join(home, ".zshrc"), []byte(o.existingRC), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	flakyN := o.flakyN
	if flakyN == 0 {
		flakyN = 2
	}
	versionFlakyN := o.versionFlakyN
	if versionFlakyN == 0 {
		versionFlakyN = 2
	}
	oldCurl := "0"
	if o.oldCurl {
		oldCurl = "1"
	}

	// Built from scratch, not from os.Environ(): the real HOME must not be able
	// to reach the script even by accident.
	env := []string{
		"PATH=" + binDir + ":/usr/bin:/bin:/usr/sbin:/sbin",
		"HOME=" + home,
		"TMPDIR=" + tmp,
		"SHELL=/bin/zsh",
		"VC_AUTH_HOST=" + mirrorAuthHost,
		"VC_INSTALL_PI=0",
		"VC_INSTALL_YES=1",
		"FAKE_LOG=" + logPath,
		"FAKE_DIR=" + fixtures,
		"FAKE_PRIMARY=" + orDefault(o.primary, "ok"),
		"FAKE_MIRROR=" + orDefault(o.mirror, "fail"),
		"FAKE_MIRROR_SUMS=" + orDefault(o.sums, "ok"),
		// Unset means "missing" in the fake curl; spelled out here so a reader
		// of the environment sees the transitional 404 the runs default to.
		"FAKE_PRIMARY_SUMS=" + orDefault(o.primarySums, "missing"),
		"FAKE_CA=" + orDefault(o.ca, "ok"),
		"FAKE_VERSION_JSON=" + orDefault(o.versionJSON, "ok"),
		"FAKE_LATEST=" + orDefault(o.latest, "ok"),
		"FAKE_FLAKY_N=" + fmt.Sprint(flakyN),
		"FAKE_VERSION_FLAKY_N=" + fmt.Sprint(versionFlakyN),
		"FAKE_CURL_OLD=" + oldCurl,
	}
	if o.skipDownload {
		env = append(env, "VC_SKIP_DOWNLOAD=1")
	}

	cmd := exec.Command("sh", "install.sh")
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("could not run install.sh: %v\n%s", err, output)
		}
		code = exitErr.ExitCode()
	}

	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	var log []string
	for _, line := range strings.Split(string(logData), "\n") {
		if strings.TrimSpace(line) != "" {
			log = append(log, line)
		}
	}

	// Whatever the implementation does, it may not have reached a real host.
	for _, line := range log {
		for _, host := range []string{"auth.makscee.ru", "nodejs.org", "deb.nodesource.com"} {
			if strings.Contains(line, host) {
				t.Fatalf("the test run reached the real %s: %s", host, line)
			}
		}
	}

	return mirrorResult{
		code:     code,
		combined: string(output),
		log:      log,
		home:     home,
		vcPath:   filepath.Join(home, ".void-code", "bin", "vc"),
		caPath:   filepath.Join(home, ".void-code", "relay-ca.pem"),
		rcPath:   filepath.Join(home, ".zshrc"),
		tmpDir:   tmp,
	}
}

func mirrorLogLines(log []string, contains ...string) []string {
	var out []string
	for _, line := range log {
		match := true
		for _, sub := range contains {
			if !strings.Contains(line, sub) {
				match = false
				break
			}
		}
		if match {
			out = append(out, line)
		}
	}
	return out
}

func mirrorBinCalls(log []string) []string {
	var out []string
	for _, line := range mirrorLogLines(log, "releases/download/") {
		if !strings.Contains(strings.ToUpper(line), "SHA256SUMS") {
			out = append(out, line)
		}
	}
	return out
}

func mirrorSumCalls(log []string) []string {
	var out []string
	for _, line := range mirrorLogLines(log, "releases/download/") {
		if strings.Contains(strings.ToUpper(line), "SHA256SUMS") {
			out = append(out, line)
		}
	}
	return out
}

func readMirrorFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// ── the contract ─────────────────────────────────────────────────────────────

func TestShellInstallerCarriesMirrorContract(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the shell installer with command fixtures")
	}

	asset := mirrorAssetName()
	mirrorBinURL := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s",
		mirrorRepo, mirrorFixtureTag, asset)
	mirrorSumsPrefix := fmt.Sprintf("https://github.com/%s/releases/download/%s/",
		mirrorRepo, mirrorFixtureTag)

	// Element 1 — the fallback source exists and is actually built. Behaviour,
	// not wording: the installed bytes are the mirror's, so a mirror that is
	// merely spelled out somewhere in the file cannot pass this.
	t.Run("mirror completes the install when the primary host does not deliver", func(t *testing.T) {
		r := runMirrorInstall(t, mirrorOpts{primary: "fail", mirror: "ok", sums: "ok"})

		if r.code != 0 {
			t.Fatalf("installer exited %d with a healthy mirror available\n%s", r.code, r.combined)
		}
		if got := readMirrorFile(t, r.vcPath); got != mirrorMirrorBytes {
			t.Errorf("vc was not installed from the mirror: got %.32q…", got)
		}
		info, err := os.Stat(r.vcPath)
		if err != nil {
			t.Fatalf("no vc installed: %v\n%s", err, r.combined)
		}
		if info.Mode().Perm()&0o111 == 0 {
			t.Errorf("installed vc is not executable: mode %v", info.Mode().Perm())
		}

		calls := mirrorBinCalls(r.log)
		if len(calls) == 0 {
			t.Fatalf("no GitHub release download was attempted\nlog:\n%s", strings.Join(r.log, "\n"))
		}
		// The release the primary host named is the release the mirror must
		// serve: the version.json tag arrived, so a different one installs
		// bytes nobody asked for.
		if !strings.Contains(strings.Join(calls, "\n"), mirrorBinURL) {
			t.Errorf("mirror URL is not %s\nlog:\n%s", mirrorBinURL, strings.Join(calls, "\n"))
		}
	})

	// Element 2 — the mirror's bytes are sha256-checked BEFORE anything replaces
	// vc. Behaviour: an existing binary is the witness — it must survive a
	// mismatch byte for byte.
	t.Run("mirror bytes are sha256-verified before anything replaces vc", func(t *testing.T) {
		sentinel := "SENTINEL-" + strings.Repeat("s", 2048) + "\n"
		r := runMirrorInstall(t, mirrorOpts{
			primary:    "fail",
			mirror:     "ok",
			sums:       "mismatch",
			existingVC: sentinel,
		})

		// Without these the mismatch is untested, not survived.
		if len(mirrorBinCalls(r.log)) == 0 {
			t.Fatalf("the mirror was never downloaded, so no checksum could be checked\nlog:\n%s",
				strings.Join(r.log, "\n"))
		}
		if len(mirrorSumCalls(r.log)) == 0 {
			t.Fatalf("no SHA256SUMS was fetched for the mirror download\nlog:\n%s",
				strings.Join(r.log, "\n"))
		}
		if sums := strings.Join(mirrorSumCalls(r.log), "\n"); !strings.Contains(sums, mirrorSumsPrefix) {
			t.Errorf("checksums must come from the same release as the binary (%s)\n%s", mirrorSumsPrefix, sums)
		}

		if r.code == 0 {
			t.Errorf("installer exited 0 after a sha256 mismatch\n%s", r.combined)
		}
		if got := readMirrorFile(t, r.vcPath); got != sentinel {
			t.Errorf("the working vc was replaced with unverified bytes: got %.32q…", got)
		}
		lower := strings.ToLower(r.combined)
		if !strings.Contains(lower, "sha256") && !strings.Contains(lower, "checksum") {
			t.Errorf("the refusal never mentions the checksum:\n%s", r.combined)
		}
		// "Nothing was replaced" has to mean nothing was left next to it either.
		entries, err := os.ReadDir(filepath.Dir(r.vcPath))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			if readMirrorFile(t, filepath.Join(filepath.Dir(r.vcPath), entry.Name())) == mirrorMirrorBytes {
				t.Errorf("rejected mirror bytes were left in the install dir as %q", entry.Name())
			}
		}
	})

	// Element 3 — downloads get whole-process retries, not one shot. The fake
	// curl advertises no --retry-all-errors, so curl's own --retry budget cannot
	// cover a mid-stream abort: nothing but a whole-process retry can rescue
	// this fetch, which is the failure the installer was written for.
	t.Run("a flapping primary is retried as a whole process", func(t *testing.T) {
		r := runMirrorInstall(t, mirrorOpts{
			primary: "flaky",
			flakyN:  2,
			mirror:  "fail",
			oldCurl: true,
		})

		if r.code != 0 {
			t.Fatalf("installer exited %d on a primary that recovers on the third attempt\n%s",
				r.code, r.combined)
		}
		if got := readMirrorFile(t, r.vcPath); got != mirrorPrimaryBytes {
			t.Errorf("vc was not installed from the recovered primary: got %.32q…", got)
		}
		calls := mirrorLogLines(r.log, "/vc/bin/")
		if len(calls) < 2 {
			t.Errorf("the vc download was attempted %d time(s) — a single shot is not a retry\nlog:\n%s",
				len(calls), strings.Join(r.log, "\n"))
		}
		if strings.Contains(r.combined, "option --retry-all-errors: is unknown") {
			t.Errorf("--retry-all-errors was passed to a curl that does not have it:\n%s", r.combined)
		}
	})

	// Spec §1 п.3 — cross-repository byte comparison is not something one
	// repository can do honestly, so the guard against the two copies drifting
	// is a pointer: whoever edits this file has to learn about the sibling from
	// the file, not from an incident. Source check on purpose — a comment has no
	// behaviour to observe.
	t.Run("the file points at its sibling copy in void-auth", func(t *testing.T) {
		content := readInstaller(t, "install.sh")
		for _, required := range []string{"void-auth", "/vc/install.sh"} {
			if !strings.Contains(content, required) {
				t.Errorf("install.sh never mentions %q, so the next editor cannot know the second copy exists", required)
			}
		}
	})
}
