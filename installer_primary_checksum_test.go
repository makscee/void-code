package installercontract

// install.sh: the PRIMARY download is sha256-checked too.
//
// The asymmetry this file exists to close: bytes taken from the GitHub mirror
// are verified against SHA256SUMS before anything replaces `vc`, and bytes taken
// from $AUTH_HOST — the path every user actually walks — are checked only for
// being larger than 1 KB. Two rounds of hardening went into the fallback and
// none into the front door.
//
// The reason it was never closed is a fact, not an opinion:
//
//     curl -sI https://auth.makscee.ru/vc/SHA256SUMS   →   HTTP/2 404
//
// The release publishes SHA256SUMS to the GitHub release but copies only
// `vc-*` into the served directory, so there has never been a list to check the
// primary download against. The fix is both halves at once: the release carries
// the list to $AUTH_HOST/vc/SHA256SUMS, and install.sh checks the primary
// download against it.
//
// The transitional period is the load-bearing part of the contract and the
// easiest thing to get wrong. Between merging the installer change and the next
// stable release there is no list on the host for ANYONE, so a strict check
// breaks every install in the world. Missing list, and list-without-our-entry,
// therefore SAY SO AND CONTINUE. Mismatch refuses, exactly as on the mirror.
//
// What is asserted here is behaviour: the exit code, the bytes that end up at
// ~/.void-code/bin/vc, and WHERE the fetcher went — never that install.sh grew
// a particular function. An implementation that fetches the list and then
// ignores it fails these just as an absent check does.
//
// Two subtests are guards rather than new behaviour, and are green before the
// implementation lands: criterion 5 (the healthy primary path still asks GitHub
// nothing — now including for checksums) and criterion 6 (the mirror path stays
// strict). They are here because the obvious implementation is one shared
// helper with a "soft" flag, and the obvious mistake is to let that flag reach
// the mirror.
//
// Spec: docs/superpowers/specs/2026-09-01-primary-checksum.md, criteria 1-7.
//
// Isolation contract — as in installer_mirror_test.go and for the same reason:
// no socket is ever opened, HOME and TMPDIR live inside the test's temp dir, the
// environment is built from scratch rather than inherited, and
// home_isolation_test.go re-checks the result for the whole package.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// primarySumsURL is where the list must live: beside version.json, on the same
// host the bytes came from. Not a guess — the spec names this route, and the
// release workflow assertion at the bottom pins the publisher to the same one.
var primarySumsURL = mirrorAuthHost + "/vc/SHA256SUMS"

// mirrorPrimarySumCalls returns the fetches aimed at the primary host's list.
// Deliberately separate from mirrorSumCalls, which counts only the mirror's:
// the two must never be confused for one another by an assertion.
func mirrorPrimarySumCalls(log []string) []string {
	return mirrorLogLines(log, "/vc/SHA256SUMS")
}

// The wording contract for the transitional case. The spec requires the run to
// SAY that it had nothing to check against; a run that silently installs
// unverified bytes satisfies every other assertion here, so the words are the
// only thing that can carry it. One marker, case-insensitive, so the sentence
// around it stays the implementer's to write.
const primaryUnverifiedMarker = "not verified"

func requireSaidUnverified(t *testing.T, r mirrorResult) {
	t.Helper()
	lower := strings.ToLower(r.combined)
	if !strings.Contains(lower, primaryUnverifiedMarker) {
		t.Errorf("the run never says the download was %q — the user is told nothing about installing unchecked bytes:\n%s",
			primaryUnverifiedMarker, r.combined)
	}
	if !strings.Contains(r.combined, primarySumsURL) {
		t.Errorf("the run never names %s, so the user cannot see which list was missing:\n%s",
			primarySumsURL, r.combined)
	}
	// The transitional case must not be worded as a refusal: that is the text
	// verify_sha256 prints on the mirror, and reaching for it here is the sign
	// of the strict path having been wired straight into the primary.
	if strings.Contains(lower, "refusing to install") {
		t.Errorf("a missing list is not a refusal, but the run says it is:\n%s", r.combined)
	}
}

func TestShellInstallerVerifiesThePrimaryDownloadToo(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the shell installer with command fixtures")
	}

	// ── criterion 1 ─────────────────────────────────────────────────────────
	// List present, sums agree → the install goes through, from the primary.
	t.Run("a primary download whose checksum agrees installs normally", func(t *testing.T) {
		r := runMirrorInstall(t, mirrorOpts{primary: "ok", primarySums: "ok", mirror: "ok", latest: "ok"})

		if r.code != 0 {
			t.Fatalf("installer exited %d on a primary download whose sha256 matches\n%s", r.code, r.combined)
		}
		// The list was really consulted: without this the subtest passes on an
		// implementation that never fetches anything and installs blind.
		if calls := mirrorPrimarySumCalls(r.log); len(calls) == 0 {
			t.Fatalf("no SHA256SUMS was fetched for the primary download\nlog:\n%s", strings.Join(r.log, "\n"))
		} else if !strings.Contains(strings.Join(calls, "\n"), primarySumsURL) {
			t.Errorf("the primary's checksum list must come from %s\n%s", primarySumsURL, strings.Join(calls, "\n"))
		}
		if got := readMirrorFile(t, r.vcPath); got != mirrorPrimaryBytes {
			t.Errorf("vc did not come from the primary host: got %.32q…", got)
		}
		if left := mirrorLeftovers(t, r.tmpDir); len(left) != 0 {
			t.Errorf("the checksum fetch left scratch files in TMPDIR: %s", strings.Join(left, ", "))
		}
	})

	// ── criterion 2 ─────────────────────────────────────────────────────────
	// Sums disagree → refuse. The existing vc is the witness: it must survive
	// byte for byte, and nothing may be parked beside it either.
	t.Run("a primary download whose checksum disagrees is refused and replaces nothing", func(t *testing.T) {
		sentinel := "SENTINEL-" + strings.Repeat("s", 2048) + "\n"
		r := runMirrorInstall(t, mirrorOpts{
			primary:     "ok",
			primarySums: "mismatch",
			// The mirror is healthy and correct on purpose: a refusal that
			// quietly installs the mirror's bytes instead is not a refusal, and
			// with a dead mirror this subtest could not tell the difference.
			mirror:     "ok",
			sums:       "ok",
			latest:     "ok",
			existingVC: sentinel,
		})

		if len(mirrorPrimarySumCalls(r.log)) == 0 {
			t.Fatalf("no SHA256SUMS was fetched, so no mismatch could have been found\nlog:\n%s",
				strings.Join(r.log, "\n"))
		}
		if r.code == 0 {
			t.Errorf("installer exited 0 after a sha256 mismatch on the primary download\n%s", r.combined)
		}
		if got := readMirrorFile(t, r.vcPath); got != sentinel {
			t.Errorf("the working vc was replaced with unverified bytes: got %.32q…", got)
		}
		lower := strings.ToLower(r.combined)
		if !strings.Contains(lower, "sha256") && !strings.Contains(lower, "checksum") {
			t.Errorf("the refusal never mentions the checksum:\n%s", r.combined)
		}

		// "Nothing was replaced" has to mean nothing was left next to it either
		// — neither the primary's rejected bytes nor the mirror's.
		dir := filepath.Dir(r.vcPath)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			switch readMirrorFile(t, filepath.Join(dir, entry.Name())) {
			case mirrorPrimaryBytes:
				t.Errorf("rejected primary bytes were left in the install dir as %q", entry.Name())
			case mirrorMirrorBytes:
				t.Errorf("a checksum mismatch was treated as a reason to install the mirror instead, as %q", entry.Name())
			}
		}
		mirrorRequireNoPartialBytes(t, r.home)
		if left := mirrorLeftovers(t, r.tmpDir); len(left) != 0 {
			t.Errorf("a refused install left scratch files in TMPDIR: %s", strings.Join(left, ", "))
		}
	})

	// ── criterion 3 ─────────────────────────────────────────────────────────
	// No list on the host at all — the state of the world between this merge and
	// the next stable release, for every user. Say it and carry on. A run that
	// refuses here breaks every install there is; a run that falls through to
	// the mirror turns a 404 on a small text file into a third-party download
	// nobody asked for.
	t.Run("a missing checksum list is said out loud and does not stop the install", func(t *testing.T) {
		r := runMirrorInstall(t, mirrorOpts{
			primary:     "ok",
			primarySums: "missing",
			mirror:      "ok",
			sums:        "ok",
			latest:      "ok",
		})

		if calls := mirrorPrimarySumCalls(r.log); len(calls) == 0 {
			t.Fatalf("the checksum list was never asked for, so its absence was never handled\nlog:\n%s",
				strings.Join(r.log, "\n"))
		}
		if r.code != 0 {
			t.Fatalf("installer exited %d because %s is not there yet — this breaks every install until the next release\n%s",
				r.code, primarySumsURL, r.combined)
		}
		if got := readMirrorFile(t, r.vcPath); got != mirrorPrimaryBytes {
			t.Errorf("vc did not come from the primary host: got %.32q…", got)
		}
		requireSaidUnverified(t, r)
		if calls := mirrorGitHubCalls(r.log); len(calls) != 0 {
			t.Errorf("a missing checksum list sent the run to github.com though the primary delivered:\n%s",
				strings.Join(calls, "\n"))
		}
		if left := mirrorLeftovers(t, r.tmpDir); len(left) != 0 {
			t.Errorf("a missing checksum list left scratch files in TMPDIR: %s", strings.Join(left, ", "))
		}
	})

	// ── criterion 4 ─────────────────────────────────────────────────────────
	// The list is there and simply does not name this asset. Indistinguishable
	// from an older format of the same file, so it gets criterion 3's treatment
	// and not criterion 2's — a "no entry means mismatch" reading refuses on a
	// file that never promised anything about these bytes.
	t.Run("a checksum list without our asset is said out loud and does not stop the install", func(t *testing.T) {
		r := runMirrorInstall(t, mirrorOpts{
			primary:     "ok",
			primarySums: "noentry",
			mirror:      "ok",
			sums:        "ok",
			latest:      "ok",
		})

		if calls := mirrorPrimarySumCalls(r.log); len(calls) == 0 {
			t.Fatalf("the checksum list was never asked for\nlog:\n%s", strings.Join(r.log, "\n"))
		}
		if r.code != 0 {
			t.Fatalf("installer exited %d on a checksum list that carries no entry for %s\n%s",
				r.code, mirrorAssetName(), r.combined)
		}
		if got := readMirrorFile(t, r.vcPath); got != mirrorPrimaryBytes {
			t.Errorf("vc did not come from the primary host: got %.32q…", got)
		}
		requireSaidUnverified(t, r)
		if calls := mirrorGitHubCalls(r.log); len(calls) != 0 {
			t.Errorf("an unusable checksum list sent the run to github.com though the primary delivered:\n%s",
				strings.Join(calls, "\n"))
		}
	})

	// ── criterion 5 ─────────────────────────────────────────────────────────
	// A guard, green today: the new fetch must be one more request to OUR host,
	// not a reason to start talking to GitHub on the healthy path. The existing
	// guard in installer_recovery_test.go runs with no list on the host at all;
	// this one runs with a healthy list, which is the case that has something
	// new to fetch and is therefore the case that could go wrong.
	t.Run("the primary's checksums come from our own host, never from GitHub", func(t *testing.T) {
		r := runMirrorInstall(t, mirrorOpts{
			primary:     "ok",
			primarySums: "ok",
			// Everything on the GitHub side is healthy: a pass must be a
			// decision not to go there, not a failure to arrive.
			mirror: "ok",
			sums:   "ok",
			latest: "ok",
		})

		if r.code != 0 {
			t.Fatalf("installer exited %d on a healthy primary\n%s", r.code, r.combined)
		}
		if len(mirrorPrimarySumCalls(r.log)) == 0 {
			t.Fatalf("no checksum list was fetched at all, so this guard proves nothing\nlog:\n%s",
				strings.Join(r.log, "\n"))
		}
		for _, line := range mirrorPrimarySumCalls(r.log) {
			if !strings.Contains(line, mirrorAuthHost) {
				t.Errorf("the primary's checksum list was fetched from somewhere other than %s: %s",
					mirrorAuthHost, line)
			}
		}
		if calls := mirrorGitHubCalls(r.log); len(calls) != 0 {
			t.Errorf("the healthy path issued %d request(s) to github.com:\n%s",
				len(calls), strings.Join(calls, "\n"))
		}
	})

	// ── criterion 6 ─────────────────────────────────────────────────────────
	// A guard, green today, and the one most likely to be broken by the fix: the
	// natural implementation is one helper with a "missing is fine" flag, and
	// the natural mistake is to let that flag reach the mirror. On the mirror a
	// missing list stays fatal — it is a third party, and unverified bytes from
	// a third party are worse than no install.
	//
	// installer_mirror_test.go already pins the MISMATCH case there. This is the
	// ABSENT case, which is precisely the one the primary path now tolerates.
	t.Run("on the mirror a missing checksum list is still a refusal", func(t *testing.T) {
		sentinel := "SENTINEL-" + strings.Repeat("m", 2048) + "\n"
		r := runMirrorInstall(t, mirrorOpts{
			primary:     "fail",
			primarySums: "missing",
			mirror:      "ok",
			sums:        "missing",
			latest:      "ok",
			existingVC:  sentinel,
		})

		if len(mirrorBinCalls(r.log)) == 0 {
			t.Fatalf("the mirror was never downloaded, so no missing list could be survived\nlog:\n%s",
				strings.Join(r.log, "\n"))
		}
		if len(mirrorSumCalls(r.log)) == 0 {
			t.Fatalf("no SHA256SUMS was even asked for on the mirror path\nlog:\n%s",
				strings.Join(r.log, "\n"))
		}
		if r.code == 0 {
			t.Errorf("installer exited 0 having installed mirror bytes it could not check\n%s", r.combined)
		}
		if got := readMirrorFile(t, r.vcPath); got != sentinel {
			t.Errorf("the working vc was replaced with unverified mirror bytes: got %.32q…", got)
		}
		mirrorRequireNoPartialBytes(t, r.home)
	})

	// ── a guard the criteria do not name ────────────────────────────────────
	// VC_SKIP_DOWNLOAD promises no download. The new fetch is easy to place
	// outside that block — it is a small text file and it does not feel like a
	// download — and the flag is used as a fixture by other tests in this
	// package, so a leak here would be paid for elsewhere.
	t.Run("VC_SKIP_DOWNLOAD fetches no checksum list either", func(t *testing.T) {
		r := runMirrorInstall(t, mirrorOpts{
			primary:      "ok",
			primarySums:  "ok",
			mirror:       "ok",
			sums:         "ok",
			skipDownload: true,
		})

		if r.code != 0 {
			t.Fatalf("installer exited %d with VC_SKIP_DOWNLOAD=1\n%s", r.code, r.combined)
		}
		if calls := mirrorPrimarySumCalls(r.log); len(calls) != 0 {
			t.Errorf("a checksum list was fetched despite VC_SKIP_DOWNLOAD=1:\n%s", strings.Join(calls, "\n"))
		}
	})
}

// ── criterion 7 ──────────────────────────────────────────────────────────────
//
// The installer half is useless on its own: without this, $AUTH_HOST/vc/SHA256SUMS
// stays a 404 forever and every install prints "not verified" until someone
// notices. Same instrument as TestStableReleaseSyncsInstallersAndBinariesToVoidAuth
// — a workflow file has no behaviour this package can run, so the contract is
// pinned by reading it. Scoped to the publish-auth job: the `release` job has
// generated and attached SHA256SUMS since long before this spec, and an
// assertion that could be satisfied by that older line would pass on day one.
func TestStableReleasePublishesChecksumsBesideVersionJSON(t *testing.T) {
	content := readInstaller(t, ".github/workflows/release.yml")

	start := strings.Index(content, "\n  publish-auth:")
	if start < 0 {
		t.Fatal("release.yml has no publish-auth job — the served copy is synced by something else now")
	}
	job := content[start:]

	// The one line that decides whether the file is ever served: void-auth
	// publishes what is committed, so a SHA256SUMS downloaded into the checkout
	// and left unstaged reaches nobody. `public/vc/SHA256SUMS` and not
	// `public/vc/bin/SHA256SUMS`: beside version.json is where install.sh looks.
	commit := strings.Index(job, "git add ")
	if commit < 0 {
		t.Fatal("publish-auth stages nothing — the sync no longer commits its files")
	}
	commitLine := job[commit:]
	if nl := strings.IndexByte(commitLine, '\n'); nl >= 0 {
		commitLine = commitLine[:nl]
	}
	if !strings.Contains(commitLine, "public/vc/SHA256SUMS") {
		t.Errorf("the release stages no checksum list beside version.json, so %s stays a 404:\n%s",
			"$AUTH_HOST/vc/SHA256SUMS", commitLine)
	}
	if strings.Contains(job, "public/vc/bin/SHA256SUMS") {
		t.Errorf("the checksum list is published under bin/, where install.sh does not look for it")
	}

	// It has to be obtained before it can be staged. Anything that puts the file
	// there counts — a second release-downloader step, a cp, a curl — so what is
	// asserted is that the job names it somewhere ahead of the commit.
	before := job[:commit]
	if !strings.Contains(before, "SHA256SUMS") {
		t.Errorf("publish-auth stages a checksum list it never obtains:\n%s", before)
	}

	// The binaries' glob must not be widened to sweep the list into bin/ — that
	// publishes the file to the wrong route while satisfying a naive reading of
	// "the release carries the sums".
	if !strings.Contains(job, `fileName: "vc-*"`) {
		t.Errorf("the binary download glob is no longer vc-* — check nothing but binaries lands in public/vc/bin")
	}
}

// A last, blunt one: the two ends must agree on the route. install.sh fetching
// /vc/SHA256SUMS while the workflow publishes /vc/checksums.txt passes every
// assertion above separately and works for nobody.
func TestInstallerAndReleaseAgreeOnTheChecksumRoute(t *testing.T) {
	installer := readInstaller(t, "install.sh")
	if !strings.Contains(installer, "/vc/SHA256SUMS") {
		t.Errorf("install.sh never names %s — nothing checks the primary download",
			fmt.Sprintf("$AUTH_HOST%s", "/vc/SHA256SUMS"))
	}
}
