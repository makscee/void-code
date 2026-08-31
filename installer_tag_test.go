package installercontract

// install.sh: the release tag is third-party text spliced into a URL path.
//
// The mirror is addressed by tag, and the tag arrives from two places, neither
// of them ours: the "tag" field of version.json served by $AUTH_HOST, and
// tag_name from api.github.com/repos/<repo>/releases/latest. Both are pasted
// into MIRROR_BIN_URL and MIRROR_SUMS_URL by bare substitution.
//
// There is no shell injection — the quoting in install.sh is right — but curl
// and wget both normalise `/../` per RFC 3986 before the request leaves the
// machine, so a tag carrying `..` addresses a path OUTSIDE
// releases/download/<repo>/: another account's repository, or any other file
// github.com will serve. A `/` re-aims within the host just as freely. sha256
// does not save this: SHA256SUMS is fetched from the same re-aimed directory,
// so forged bytes are checked against the forger's own list, the installer
// exits 0, and the result lands in ~/.void-code/bin/vc.
//
// The contract is a shape, not a blocklist: a tag is [A-Za-z0-9._-]+ or it is
// not a tag, and one that is not a tag is REFUSED — never trimmed, never
// stripped, never percent-encoded into something that merely looks safe.
//
// What is asserted here is therefore WHERE THE FETCHER WENT and what the script
// exited with — not whether the file grew a `case` statement. A run that never
// issues the mangled request is the only evidence that counts, so an
// implementation that builds the URL and then declines to use it fails these
// just as an absent check does.
//
// The guards at the bottom are half the point: a rule that also rejects
// `v0.2.48` deletes the mirror instead of hardening it, and would go unnoticed
// until the day the primary host is down — the one day the mirror exists for.
//
// Spec: docs/superpowers/specs/2026-08-31-installer-fallback-fixes.md.
// The sibling copy in void-auth carries the same contract in
// test/vc-install-fallback.test.ts, round 2 — a different language, a different
// harness, and a wider set of cases; nothing here is derived from it by copying.
//
// Isolation contract — as in installer_mirror_test.go, and for the same reason:
// no socket is ever opened, HOME and TMPDIR live inside the test's temp dir, the
// environment is built from scratch rather than inherited, and
// home_isolation_test.go re-checks the result for the whole package.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	// A marker that cannot occur in any URL install.sh legitimately builds, so
	// its presence in the fetch log names the tag as the source of the detour.
	tagEvilMarker = "evil-mirror"

	// Walks out of releases/download/<repo>/ entirely.
	tagTraversal = "../../../../" + tagEvilMarker + "/vc"
	// Stays on github.com and inside the release path prefix, but re-aims the
	// last segment: no `..` involved, so a check that only hunts for dot-dot
	// passes this one.
	tagSlash = "v9.9.9/" + tagEvilMarker
	// Not an absent tag: `${_vj_tag:-}` treats "" as unset and falls through to
	// the version-derived tag, but three spaces are non-empty, so they survive
	// that guard and land in the URL as spaces.
	tagBlank = "   "
	// ASCII except for one letter. Which is the whole point: the alphabet has to
	// be spelled out character by character rather than written as A-Z / a-z
	// ranges, because a bracket range in a `case` glob is resolved through the
	// locale's COLLATION, and in a collation where é sorts between e and f the
	// range a-z contains it. See tagRangeBlindLocale below.
	tagNonASCII = "vé1.0" // vé1.0

	// The auth host for these runs. Deliberately NOT the neighbour's
	// auth.test.invalid: the rejection assertion below looks for the word
	// "invalid" in the output, and a host carrying it in its own name satisfies
	// that assertion on every run, including runs where nothing was rejected at
	// all. .example is reserved by RFC 2606 just as .invalid is.
	tagAuthHost = "https://auth.test.example"

	// Ordinary tags, the shape the mirror is actually addressed by.
	tagGood = "v9.9.9"
	// Every character class the alphabet allows: letters, digits, dot, dash,
	// underscore. Real prereleases look like this.
	tagGoodExotic = "v9.9.9-rc.1_beta"
)

type tagOpts struct {
	versionJSON    string // ok | fail
	versionTag     string // value of the "tag" field; empty → tagGood
	omitVersionTag bool   // write version.json with no "tag" field at all
	latest         string // ok | fail
	latestTag      string // value of "tag_name"; empty → tagGood
	primary        string // ok | fail | flaky
	mirror         string // ok | fail
	sums           string // ok | fail | mismatch
	dryRun         bool
	locale         string // LC_ALL/LANG for the run; empty → C
}

// runTagInstall runs install.sh against fake fetchers, with both tag sources
// under the test's control. Same fixtures and same fake executables as
// runMirrorInstall — only the two tag values are steerable here, so a failure
// can be blamed on nothing else.
func runTagInstall(t *testing.T, o tagOpts) mirrorResult {
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
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	writeFixture := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(fixtures, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	asset := mirrorAssetName()
	tagField := ""
	if !o.omitVersionTag {
		tagField = fmt.Sprintf("  \"tag\": %q,\n", orDefault(o.versionTag, tagGood))
	}
	writeFixture("version.json", fmt.Sprintf(`{
  "version": %q,
%s  "artifacts": {
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
`, mirrorFixtureVersion, tagField))
	writeFixture("latest-api.json", fmt.Sprintf(`{
  "tag_name": %q,
  "draft": false,
  "prerelease": false
}
`, orDefault(o.latestTag, tagGood)))
	writeFixture("relay-ca.pem", "-----BEGIN CERTIFICATE-----\nZmFrZQ==\n-----END CERTIFICATE-----\n")
	writeFixture("primary-bin", mirrorPrimaryBytes)
	writeFixture("mirror-bin", mirrorMirrorBytes)
	// The mirror's SHA256SUMS matches the mirror's bytes: verification is not
	// what is under test here, so it must not be what stops a hostile tag.
	writeFixture("sums-mirror", fmt.Sprintf("%s  %s\n", mirrorSHA256(mirrorMirrorBytes), asset))
	writeFixture("sums-mirror-bad", fmt.Sprintf("%s  %s\n", strings.Repeat("0", 64), asset))
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

	env := []string{
		"PATH=" + binDir + ":/usr/bin:/bin:/usr/sbin:/sbin",
		"HOME=" + home,
		"TMPDIR=" + tmp,
		"SHELL=/bin/zsh",
		// Explicit, not inherited and not left to the shell's default: the
		// character rule this file pins is locale-sensitive, so the locale is
		// part of every fixture rather than a property of the machine.
		"LC_ALL=" + orDefault(o.locale, "C"),
		"LANG=" + orDefault(o.locale, "C"),
		"VC_AUTH_HOST=" + tagAuthHost,
		"VC_INSTALL_PI=0",
		"VC_INSTALL_YES=1",
		"FAKE_LOG=" + logPath,
		"FAKE_DIR=" + fixtures,
		"FAKE_PRIMARY=" + orDefault(o.primary, "ok"),
		"FAKE_MIRROR=" + orDefault(o.mirror, "fail"),
		"FAKE_MIRROR_SUMS=" + orDefault(o.sums, "ok"),
		"FAKE_CA=ok",
		"FAKE_VERSION_JSON=" + orDefault(o.versionJSON, "ok"),
		"FAKE_LATEST=" + orDefault(o.latest, "ok"),
		"FAKE_FLAKY_N=2",
		"FAKE_CURL_OLD=0",
	}
	if o.dryRun {
		env = append(env, "VC_INSTALL_DRY_RUN=1")
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
	}
}

// tagLatestCalls returns the fetches aimed at GitHub's latest-release lookup.
func tagLatestCalls(log []string) []string {
	return mirrorLogLines(log, "releases/latest")
}

// tagRequireNoMangledFetch fails if any fetch the run actually issued carries a
// mangled tag. The log holds curl invocations and nothing else, and every URL
// install.sh legitimately builds is free of both markers, so a hit here can only
// have come from the tag.
func tagRequireNoMangledFetch(t *testing.T, log []string) {
	t.Helper()
	for _, line := range log {
		if strings.Contains(line, "..") {
			t.Errorf("a request was issued down a path the tag bent out of the release dir:\n  %s", line)
		}
		if strings.Contains(line, tagEvilMarker) {
			t.Errorf("a request was issued to the attacker's target %q:\n  %s", tagEvilMarker, line)
		}
	}
}

// A refusal nobody can read is a refusal nobody can act on: some line of output
// has to say that a tag was rejected. Deliberately loose about wording, strict
// about substance.
var tagRejectionRe = regexp.MustCompile(`(?i)reject|refus|invalid|malformed|not a tag|not a valid|unusable`)

func tagRequireSaysRejected(t *testing.T, combined string) {
	t.Helper()
	for _, line := range strings.Split(combined, "\n") {
		if strings.Contains(strings.ToLower(line), "tag") && tagRejectionRe.MatchString(line) {
			return
		}
	}
	t.Errorf("no output line says the release tag was rejected:\n%s", combined)
}

// ── choosing a locale that can tell the two spellings apart ──────────────────
//
// The rule "a tag is [A-Za-z0-9._-]+" has two spellings in shell, and they are
// not the same rule:
//
//	case "$1" in *[!A-Za-z0-9._-]*) ...          # ranges
//	case "$1" in *[!ABC…abc…0123456789._-]*) ... # spelled out
//
// A bracket RANGE is resolved through the locale's collation. Measured on this
// machine (bash 3.2 as /bin/sh) with the tag `vé1.0`:
//
//	LC_ALL=C            ranges reject   spelled out reject
//	LC_ALL=en_US.UTF-8  ranges ACCEPT   spelled out reject
//	LC_ALL=C.UTF-8      ranges reject   spelled out reject
//	LC_ALL=ru_RU.UTF-8  ranges reject   spelled out reject
//
// So the difference is real, and "run it in some UTF-8 locale" is not enough to
// see it: two of the three UTF-8 locales above cannot tell the spellings apart.
// A subtest pinned to a locale picked by name would go green on those machines
// while testing nothing — the same class of "не смог" dressed as "прошло" that
// this file exists to avoid.
//
// Hence the probe: ask THIS machine's `sh`, in each locale it actually has,
// whether a range admits a non-ASCII letter. Only a locale that answers yes can
// discriminate, and only such a locale is used.

// tagRangeBlindLocale finds a locale in which this machine's `sh` treats a
// bracket range as collation-based. Returns the locale, the candidates tried,
// and whether one was found.
func tagRangeBlindLocale(t *testing.T) (locale string, tried []string, ok bool) {
	t.Helper()

	// en_US.UTF-8 first because it is the usual one; then whatever the machine
	// reports, so a box without it is not written off.
	seen := map[string]bool{}
	candidates := []string{"en_US.UTF-8", "en_US.utf8"}
	for _, c := range candidates {
		seen[c] = true
	}
	if out, err := exec.Command("locale", "-a").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			name := strings.TrimSpace(line)
			if name == "" || seen[name] {
				continue
			}
			norm := strings.ToLower(strings.ReplaceAll(name, "-", ""))
			if !strings.HasSuffix(norm, ".utf8") {
				continue
			}
			seen[name] = true
			candidates = append(candidates, name)
		}
	}

	// Does `sh` in this locale say the tag matches the given bracket set?
	matches := func(loc, set string) (bool, bool) {
		script := fmt.Sprintf(`case "$1" in *[!%s]*) printf outside ;; *) printf inside ;; esac`, set)
		cmd := exec.Command("sh", "-c", script, "sh", tagNonASCII)
		cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LC_ALL=" + loc, "LANG=" + loc}
		out, err := cmd.Output()
		if err != nil {
			return false, false
		}
		switch string(out) {
		case "inside":
			return true, true
		case "outside":
			return false, true
		}
		return false, false
	}

	const ranges = `A-Za-z0-9._-`
	const spelled = `ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._-`

	for _, loc := range candidates {
		rangeAdmits, ranOK := matches(loc, ranges)
		if !ranOK || !rangeAdmits {
			continue
		}
		// The locale must also leave the spelled-out alphabet strict. If both
		// spellings admitted the letter, the contract would be unsatisfiable in
		// this locale and a red subtest would be blaming the implementation for
		// the fixture's choice.
		spelledAdmits, ranOK := matches(loc, spelled)
		if !ranOK || spelledAdmits {
			continue
		}
		return loc, candidates, true
	}
	return "", candidates, false
}

// tagRequireNothingInstalled: no vc, and no mirror bytes parked next to where it
// would have gone. "Refused" has to mean the bytes are not on disk at all.
func tagRequireNothingInstalled(t *testing.T, r mirrorResult) {
	t.Helper()
	if _, err := os.Stat(r.vcPath); err == nil {
		t.Errorf("vc was installed from a refused tag: %s contains %.32q…", r.vcPath, readMirrorFile(t, r.vcPath))
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat %s: %v", r.vcPath, err)
	}
	dir := filepath.Dir(r.vcPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if readMirrorFile(t, filepath.Join(dir, entry.Name())) == mirrorMirrorBytes {
			t.Errorf("bytes fetched under a refused tag were left in the install dir as %q", entry.Name())
		}
	}
}

// ── the contract ─────────────────────────────────────────────────────────────

func TestShellInstallerRefusesAReleaseTagThatIsNotATag(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the shell installer with command fixtures")
	}

	// The mirror is healthy in every subtest below, and its checksums match its
	// bytes. So nothing except the tag rule can stop these installs — if the
	// script exits 0 here, it exited 0 on bytes chosen by whoever wrote the tag.

	// Path 1 — version.json on $AUTH_HOST.
	//
	// releases/latest is dead in these on purpose: version.json is then the only
	// tag source in the run, so nothing can rescue the install and the exit code
	// is unambiguous whether the implementation aborts on the spot or treats the
	// rejected tag as "no tag" and falls through.
	t.Run("a version.json tag carrying .. is refused, not walked out of the release path", func(t *testing.T) {
		r := runTagInstall(t, tagOpts{
			versionTag: tagTraversal,
			primary:    "fail",
			latest:     "fail",
			mirror:     "ok",
			sums:       "ok",
		})

		tagRequireNoMangledFetch(t, r.log)
		// Not one release URL was built from it — not for the binary, not for
		// the checksum list.
		if calls := append(mirrorBinCalls(r.log), mirrorSumCalls(r.log)...); len(calls) != 0 {
			t.Errorf("a release URL was fetched from a tag that is not a tag:\n%s", strings.Join(calls, "\n"))
		}
		if r.code == 0 {
			t.Errorf("installer exited 0 on a tag that addresses a path outside the release\n%s", r.combined)
		}
		tagRequireNothingInstalled(t, r)
		tagRequireSaysRejected(t, r.combined)
	})

	t.Run("a version.json tag that is only whitespace is refused", func(t *testing.T) {
		r := runTagInstall(t, tagOpts{
			versionTag: tagBlank,
			primary:    "fail",
			latest:     "fail",
			mirror:     "ok",
			sums:       "ok",
		})

		if calls := append(mirrorBinCalls(r.log), mirrorSumCalls(r.log)...); len(calls) != 0 {
			t.Errorf("a release URL was built out of blank space:\n%s", strings.Join(calls, "\n"))
		}
		if r.code == 0 {
			t.Errorf("installer exited 0 on a tag made of spaces\n%s", r.combined)
		}
		tagRequireNothingInstalled(t, r)
	})

	// Path 2 — tag_name from api.github.com/repos/<repo>/releases/latest.
	//
	// version.json is dead in these, which is the only way this path is reached
	// at all: the latest-release lookup exists precisely for the case where the
	// primary host cannot supply a tag.
	t.Run("a releases/latest tag_name carrying .. is refused", func(t *testing.T) {
		r := runTagInstall(t, tagOpts{
			versionJSON: "fail",
			latest:      "ok",
			latestTag:   tagTraversal,
			primary:     "fail",
			mirror:      "ok",
			sums:        "ok",
		})

		// Without this the subtest could pass by never reaching the path at all.
		if len(tagLatestCalls(r.log)) == 0 {
			t.Fatalf("the latest-release lookup was never made, so its tag was never under test\nlog:\n%s",
				strings.Join(r.log, "\n"))
		}
		tagRequireNoMangledFetch(t, r.log)
		if calls := append(mirrorBinCalls(r.log), mirrorSumCalls(r.log)...); len(calls) != 0 {
			t.Errorf("a release URL was fetched from a tag_name that is not a tag:\n%s", strings.Join(calls, "\n"))
		}
		if r.code == 0 {
			t.Errorf("installer exited 0 on a tag_name that addresses a path outside the release\n%s", r.combined)
		}
		tagRequireNothingInstalled(t, r)
		tagRequireSaysRejected(t, r.combined)
	})

	t.Run("a releases/latest tag_name carrying a slash is refused", func(t *testing.T) {
		r := runTagInstall(t, tagOpts{
			versionJSON: "fail",
			latest:      "ok",
			latestTag:   tagSlash,
			primary:     "fail",
			mirror:      "ok",
			sums:        "ok",
		})

		if len(tagLatestCalls(r.log)) == 0 {
			t.Fatalf("the latest-release lookup was never made, so its tag was never under test\nlog:\n%s",
				strings.Join(r.log, "\n"))
		}
		tagRequireNoMangledFetch(t, r.log)
		if calls := append(mirrorBinCalls(r.log), mirrorSumCalls(r.log)...); len(calls) != 0 {
			t.Errorf("a release URL was fetched from a tag_name that re-aims with a slash:\n%s",
				strings.Join(calls, "\n"))
		}
		if r.code == 0 {
			t.Errorf("installer exited 0 on a tag_name that re-aims the release path\n%s", r.combined)
		}
		tagRequireNothingInstalled(t, r)
		tagRequireSaysRejected(t, r.combined)
	})

	// The character rule has to hold in the locale the user is actually in, not
	// only in C. Every other subtest here runs in C, where the two ways of
	// spelling the alphabet — ranges and character-by-character — behave
	// identically; a UTF-8 locale is where they part company, and where a tag
	// like `vé1.0` gets into the URL under the range spelling. The comment in
	// install.sh says as much; this is the measurement behind it.
	t.Run("a non-ASCII tag is refused in a UTF-8 locale as well", func(t *testing.T) {
		locale, tried, ok := tagRangeBlindLocale(t)
		if !ok {
			// NOT a pass. There is no locale on this machine in which `sh`
			// resolves a bracket range through collation, so this subtest can
			// only produce a green that means nothing — and a green that means
			// nothing is the failure mode this whole file is written against.
			t.Skipf("НЕ СМОГ: ни в одной локали этой машины `sh` не разрешает диапазон "+
				"через collation, поэтому подтест не отличает [A-Za-z0-9._-] от "+
				"посимвольного алфавита и ничего не проверяет.\n"+
				"    Пробовал (%d): %s\n"+
				"    Это НЕ подтверждение правила — на такой машине оно остаётся непроверенным.",
				len(tried), strings.Join(tried, ", "))
		}
		t.Logf("locale under test: %s", locale)

		r := runTagInstall(t, tagOpts{
			versionTag: tagNonASCII,
			primary:    "fail",
			latest:     "fail",
			mirror:     "ok",
			sums:       "ok",
			locale:     locale,
		})

		for _, line := range r.log {
			if strings.Contains(line, "é") {
				t.Errorf("a request was issued carrying a non-ASCII tag (locale %s):\n  %s", locale, line)
			}
		}
		if calls := append(mirrorBinCalls(r.log), mirrorSumCalls(r.log)...); len(calls) != 0 {
			t.Errorf("a release URL was built from a non-ASCII tag in locale %s:\n%s",
				locale, strings.Join(calls, "\n"))
		}
		if r.code == 0 {
			t.Errorf("installer exited 0 on the tag %q in locale %s\n%s", tagNonASCII, locale, r.combined)
		}
		tagRequireNothingInstalled(t, r)
		tagRequireSaysRejected(t, r.combined)
	})

	// The dry run's whole job is to print the URLs a real run would fetch. A
	// refusal that only happens at download time still hands the user a
	// github.com URL pointing outside the release and invites them to run it by
	// hand — so the check belongs where the value is received, not where it is
	// used. Quoting the rejected value back is fine; offering it as a URL is not.
	t.Run("the dry run never offers the mangled URL either", func(t *testing.T) {
		r := runTagInstall(t, tagOpts{
			versionTag: tagTraversal,
			latest:     "fail",
			dryRun:     true,
		})

		if r.code != 0 {
			t.Fatalf("dry run exited %d\n%s", r.code, r.combined)
		}
		for _, line := range strings.Split(r.combined, "\n") {
			if !strings.Contains(line, "releases/download") {
				continue
			}
			if strings.Contains(line, "..") || strings.Contains(line, tagEvilMarker) {
				t.Errorf("the dry run printed a release URL bent by the tag, for a human to paste:\n  %s", line)
			}
		}
		tagRequireSaysRejected(t, r.combined)
	})
}

// ── the guards ───────────────────────────────────────────────────────────────
//
// These four pass today and must still pass afterwards. They are the half of
// the contract that a too-wide rule breaks: refusing `v0.2.48`, or treating an
// absent tag as a hostile one, removes the mirror rather than hardening it, and
// the loss shows up only on the day the primary host is down.

func TestShellInstallerStillAcceptsAnOrdinaryReleaseTag(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the shell installer with command fixtures")
	}

	asset := mirrorAssetName()
	mirrorURL := func(tag, name string) string {
		return fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", mirrorRepo, tag, name)
	}

	requireInstalledFromMirror := func(t *testing.T, r mirrorResult, tag string) {
		t.Helper()
		if r.code != 0 {
			t.Fatalf("installer exited %d on the ordinary tag %q\n%s", r.code, tag, r.combined)
		}
		if got := readMirrorFile(t, r.vcPath); got != mirrorMirrorBytes {
			t.Errorf("vc was not installed from the mirror: got %.32q…", got)
		}
		if bins := strings.Join(mirrorBinCalls(r.log), "\n"); !strings.Contains(bins, mirrorURL(tag, asset)) {
			t.Errorf("the binary was not fetched from %s\nlog:\n%s", mirrorURL(tag, asset), bins)
		}
		if sums := strings.Join(mirrorSumCalls(r.log), "\n"); !strings.Contains(sums, mirrorURL(tag, "SHA256SUMS")) {
			t.Errorf("the checksums were not fetched from %s\nlog:\n%s", mirrorURL(tag, "SHA256SUMS"), sums)
		}
	}

	t.Run("an ordinary version.json tag still reaches the mirror", func(t *testing.T) {
		r := runTagInstall(t, tagOpts{versionTag: tagGood, primary: "fail", mirror: "ok", sums: "ok"})
		requireInstalledFromMirror(t, r, tagGood)
	})

	// Letters, digits, dot, dash and underscore all at once: a rule written as
	// `v[0-9.]+` would pass the subtest above and still refuse every prerelease
	// makscee/void-code has ever published.
	t.Run("a prerelease tag with dashes, dots and underscores still reaches the mirror", func(t *testing.T) {
		r := runTagInstall(t, tagOpts{versionTag: tagGoodExotic, primary: "fail", mirror: "ok", sums: "ok"})
		requireInstalledFromMirror(t, r, tagGoodExotic)
	})

	t.Run("an ordinary releases/latest tag_name still reaches the mirror", func(t *testing.T) {
		r := runTagInstall(t, tagOpts{
			versionJSON: "fail",
			latest:      "ok",
			latestTag:   tagGood,
			primary:     "fail",
			mirror:      "ok",
			sums:        "ok",
		})
		if len(tagLatestCalls(r.log)) == 0 {
			t.Fatalf("the latest-release lookup was never made\nlog:\n%s", strings.Join(r.log, "\n"))
		}
		requireInstalledFromMirror(t, r, tagGood)
	})

	// An ABSENT tag is not a refused one. version.json without a "tag" field is
	// an ordinary older document, not an attack, and the tag derived from its
	// version field is built by install.sh out of digits it also carries — so
	// the mirror must still work. This is the case the refusal is most likely to
	// swallow by accident, because "" and "   " look alike from inside a check
	// that trims.
	t.Run("version.json with no tag field still derives one and reaches the mirror", func(t *testing.T) {
		r := runTagInstall(t, tagOpts{
			omitVersionTag: true,
			latest:         "fail",
			primary:        "fail",
			mirror:         "ok",
			sums:           "ok",
		})
		requireInstalledFromMirror(t, r, "v"+mirrorFixtureVersion)
	})
}
