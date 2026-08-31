package installercontract

// install.sh: what a failed install is allowed to leave behind, and what a
// successful one is not allowed to touch.
//
// installer_mirror_test.go pins the mirror contract and installer_tag_test.go
// pins the tag rule. Both are about the path that WORKS. This file is about the
// edges around it — the cases where the point is that nothing was damaged:
//
//   1. a failed CA fetch never damages a working install (spec §3): an existing
//      relay-ca.pem survives byte for byte, and where there was none, nothing
//      is left at the final path — not an empty file, not a partial one;
//   2. both sources down: non-zero exit, no `vc`, no scratch file parked in
//      TMPDIR (on the real thing that is ~8 MB per failed attempt);
//   3. a healthy primary asks GitHub nothing at all — no releases/latest
//      lookup, no release download, not one request to the host;
//   4. version.json is retried as a WHOLE PROCESS, not once. It carries the tag
//      the mirror is addressed by, so a single-shot fetch here kills the
//      fallback in exactly the case the fallback exists for;
//   5. VC_SKIP_DOWNLOAD downloads no binary from either source and leaves the
//      user's rc file alone.
//
// Spec: docs/superpowers/specs/2026-08-31-installer-fallback-fixes.md §2 and §3.
// The sibling copy in void-auth covers the same ground in
// test/vc-install-fallback.test.ts (criteria 1, 3, 6 and the "fixes §2/§3"
// blocks) — a different language and a different harness; nothing here is
// derived from it by copying.
//
// Isolation contract — as in installer_mirror_test.go and for the same reason:
// no socket is opened, HOME and TMPDIR live inside the test's temp dir, the
// environment is built from scratch rather than inherited, and
// home_isolation_test.go re-checks the result for the whole package.

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func mirrorPrimaryBinCalls(log []string) []string {
	return mirrorLogLines(log, "/vc/bin/")
}

func mirrorVersionJSONCalls(log []string) []string {
	return mirrorLogLines(log, "/vc/version.json")
}

func mirrorLatestCalls(log []string) []string {
	return mirrorLogLines(log, "releases/latest")
}

func mirrorGitHubCalls(log []string) []string {
	return mirrorLogLines(log, "github.com")
}

// mirrorLeftovers lists what the run left in its private TMPDIR. install.sh's
// own mktemp is the only writer there — the fake mktemp resolves to $TMPDIR and
// nothing else in the harness puts a file in it — so a non-empty result names a
// scratch file the script did not clean up.
func mirrorLeftovers(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var out []string
	for _, entry := range entries {
		out = append(out, entry.Name())
	}
	sort.Strings(out)
	return out
}

// mirrorRequireNoPartialBytes: nothing anywhere under ~/.void-code may hold the
// bytes a mid-stream abort dumps — not at a final path, not at a `.tmp`
// sibling, not anywhere. Checked by content rather than by name, because the
// name a botched fix leaves them under is exactly what nobody can predict.
func mirrorRequireNoPartialBytes(t *testing.T, home string) {
	t.Helper()
	root := filepath.Join(home, ".void-code")
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		if bytes.Contains(data, []byte("PARTIAL-")) {
			rel, _ := filepath.Rel(home, path)
			t.Errorf("bytes from an aborted download were left under the install dir as ~/%s", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// ── 1. a failed CA fetch never damages a working install ─────────────────────
//
// The CA used to be fetched straight to its final path, and fetch_to_file_retry
// truncates its destination when it runs out of attempts. So a re-run on a
// flapping network blanked a relay-ca.pem that had been working a minute
// earlier and left the machine worse than before the installer ran. The fix
// (CA_TMP → mv) has no test in this repository at all: every fixture here
// hardcodes FAKE_CA=ok, so the truncate bug could walk straight back in under a
// green `go test ./...`.

func TestShellInstallerNeverDamagesAnExistingRelayCA(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the shell installer with command fixtures")
	}

	// The exit code is deliberately not asserted in the first subtest: whether a
	// machine that already trusts the CA should still fail the run is the
	// implementation's call. What is NOT its call is the existing bytes.
	t.Run("a working relay-ca.pem survives a failed download byte for byte", func(t *testing.T) {
		// Long enough that a truncated or partially-rewritten file cannot
		// coincide with it, and marked so a survivor is identifiable.
		existing := "-----BEGIN CERTIFICATE-----\nWORKING-CA-" + strings.Repeat("k", 3072) +
			"\n-----END CERTIFICATE-----\n"

		r := runMirrorInstall(t, mirrorOpts{primary: "ok", ca: "fail", existingCA: existing})

		// Without these the subtest passes on a run that never went near the CA.
		if len(mirrorLogLines(r.log, "/vc/relay-ca.pem")) == 0 {
			t.Fatalf("the relay CA was never fetched, so no failure was survived\nlog:\n%s",
				strings.Join(r.log, "\n"))
		}
		lower := strings.ToLower(r.combined)
		if !strings.Contains(lower, "relay ca") && !strings.Contains(lower, "relay-ca") {
			t.Errorf("the run never says a word about the relay CA it failed to fetch:\n%s", r.combined)
		}

		got, err := os.ReadFile(r.caPath)
		if err != nil {
			t.Fatalf("the existing relay CA is gone after a failed refresh: %v\n%s", err, r.combined)
		}
		if string(got) != existing {
			t.Errorf("the existing relay CA was damaged by a failed refresh: %d bytes left of %d, starts %.40q",
				len(got), len(existing), string(got))
		}

		mirrorRequireNoPartialBytes(t, r.home)
		if left := mirrorLeftovers(t, r.tmpDir); len(left) != 0 {
			t.Errorf("the CA fetch left scratch files behind: %s", strings.Join(left, ", "))
		}
	})

	t.Run("no relay-ca.pem is left at all when there was none to keep", func(t *testing.T) {
		r := runMirrorInstall(t, mirrorOpts{primary: "ok", ca: "fail"})

		if r.code == 0 {
			t.Errorf("installer exited 0 with no relay CA installed at all\n%s", r.combined)
		}
		if !strings.Contains(strings.ToLower(r.combined), "failed to download relay ca") {
			t.Errorf("the failure never names the relay CA download:\n%s", r.combined)
		}

		// Not "empty" — absent. An empty relay-ca.pem is a file vc reads and
		// chokes on, and it satisfies every check that only tests existence.
		if info, err := os.Stat(r.caPath); err == nil {
			t.Errorf("a relay-ca.pem was left at %s (%d bytes) though none could be downloaded",
				r.caPath, info.Size())
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat %s: %v", r.caPath, err)
		}

		mirrorRequireNoPartialBytes(t, r.home)
		if left := mirrorLeftovers(t, r.tmpDir); len(left) != 0 {
			t.Errorf("the failed CA fetch left scratch files behind: %s", strings.Join(left, ", "))
		}
	})
}

// ── 2. both sources down ─────────────────────────────────────────────────────

func TestShellInstallerLeavesNothingWhenNeitherSourceDelivers(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the shell installer with command fixtures")
	}

	t.Run("the mirror is tried, the run fails, and no vc is left behind", func(t *testing.T) {
		r := runMirrorInstall(t, mirrorOpts{primary: "fail", mirror: "fail"})

		// The mirror really was reached for: a run that gives up before trying
		// it proves nothing about what a total failure leaves behind, and it
		// would also be the very regression the mirror contract exists to stop.
		if len(mirrorPrimaryBinCalls(r.log)) == 0 {
			t.Fatalf("the primary binary was never fetched\nlog:\n%s", strings.Join(r.log, "\n"))
		}
		if len(mirrorBinCalls(r.log)) == 0 {
			t.Errorf("the mirror was never tried when the primary was dead\nlog:\n%s",
				strings.Join(r.log, "\n"))
		}

		if r.code == 0 {
			t.Errorf("installer exited 0 with no binary from either source\n%s", r.combined)
		}
		if !strings.Contains(strings.ToLower(r.combined), "failed to download") {
			t.Errorf("the failure is not stated in the output:\n%s", r.combined)
		}

		if _, err := os.Stat(r.vcPath); err == nil {
			t.Errorf("a vc was installed though no source delivered one: %s holds %.32q…",
				r.vcPath, readMirrorFile(t, r.vcPath))
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat %s: %v", r.vcPath, err)
		}
		mirrorRequireNoPartialBytes(t, r.home)

		// A failed install must not park the aborted download in TMPDIR either.
		// On the real thing that is an ~8 MB file per failed attempt, and the
		// user has no reason to ever look for it.
		if left := mirrorLeftovers(t, r.tmpDir); len(left) != 0 {
			t.Errorf("a failed install left scratch files in TMPDIR: %s", strings.Join(left, ", "))
		}
	})
}

// ── 3. a healthy primary asks GitHub nothing ─────────────────────────────────
//
// Every other subtest in this package walks the mirror path, so "the mirror is
// a fallback and not a second source" is asserted by nobody. It is a real
// contract and not a nicety: the mirror is a third party, and a healthy install
// that quietly also talks to github.com hands that third party a request it
// was never meant to see — and hands the user a dependency they were told they
// did not have.

func TestShellInstallerAsksGitHubNothingWhenThePrimaryIsHealthy(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the shell installer with command fixtures")
	}

	t.Run("a healthy primary resolves no latest release and fetches no mirror", func(t *testing.T) {
		// The mirror is HEALTHY here, and so is the latest-release lookup: if
		// either were dead the run could pass by failing to reach them rather
		// than by declining to.
		r := runMirrorInstall(t, mirrorOpts{primary: "ok", mirror: "ok", sums: "ok", latest: "ok"})

		if r.code != 0 {
			t.Fatalf("installer exited %d on a healthy primary\n%s", r.code, r.combined)
		}
		if got := readMirrorFile(t, r.vcPath); got != mirrorPrimaryBytes {
			t.Errorf("vc did not come from the primary host: got %.32q…", got)
		}
		if len(mirrorPrimaryBinCalls(r.log)) == 0 {
			t.Fatalf("the primary binary was never fetched\nlog:\n%s", strings.Join(r.log, "\n"))
		}

		if calls := mirrorLatestCalls(r.log); len(calls) != 0 {
			t.Errorf("GitHub's latest-release lookup was made though the primary was healthy:\n%s",
				strings.Join(calls, "\n"))
		}
		if calls := mirrorBinCalls(r.log); len(calls) != 0 {
			t.Errorf("a release asset was downloaded though the primary was healthy:\n%s",
				strings.Join(calls, "\n"))
		}
		if calls := mirrorSumCalls(r.log); len(calls) != 0 {
			t.Errorf("a release SHA256SUMS was downloaded though the primary was healthy:\n%s",
				strings.Join(calls, "\n"))
		}
		// The two above name the paths we know about. This one catches a
		// request to github.com that takes some shape nobody has thought of.
		if calls := mirrorGitHubCalls(r.log); len(calls) != 0 {
			t.Errorf("the healthy path issued %d request(s) to github.com:\n%s",
				len(calls), strings.Join(calls, "\n"))
		}

		if left := mirrorLeftovers(t, r.tmpDir); len(left) != 0 {
			t.Errorf("a successful install left scratch files in TMPDIR: %s", strings.Join(left, ", "))
		}
	})
}

// ── 4. version.json survives a flapping host ─────────────────────────────────

func TestShellInstallerRetriesVersionJSONAsAWholeProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the shell installer with command fixtures")
	}

	// Old curl on purpose in both subtests: without --retry-all-errors curl's
	// own --retry budget does not cover a mid-stream abort, so nothing but a
	// whole-process retry can rescue this fetch. A run that passes here has
	// therefore re-run the fetcher, not merely handed it a flag.

	t.Run("a flapping version.json is fetched again, and its contents are used", func(t *testing.T) {
		r := runMirrorInstall(t, mirrorOpts{
			oldCurl:       true,
			versionJSON:   "flaky",
			versionFlakyN: 2,
			primary:       "ok",
		})

		if r.code != 0 {
			t.Fatalf("installer exited %d on a version.json that recovers on the third attempt\n%s",
				r.code, r.combined)
		}
		if calls := mirrorVersionJSONCalls(r.log); len(calls) < 3 {
			t.Errorf("version.json was fetched %d time(s) — a single shot is not a retry\nlog:\n%s",
				len(calls), strings.Join(r.log, "\n"))
		}
		// It arrived AND was read: the banner carries the version only when the
		// document was parsed, so this rules out a run that retried and then
		// threw the answer away.
		if !strings.Contains(r.combined, "(v"+mirrorFixtureVersion+")") {
			t.Errorf("version.json arrived but its version never reached the banner:\n%s", r.combined)
		}
		if left := mirrorLeftovers(t, r.tmpDir); len(left) != 0 {
			t.Errorf("a retried version.json left scratch files behind: %s", strings.Join(left, ", "))
		}
	})

	// The tag is the whole reason this fetch is worth retrying. The two tag
	// sources carry different tags in these fixtures on purpose, so the mirror
	// URL says which one the run actually used: v9.9.9 can only have come from
	// version.json, v8.8.8 only from the latest-release lookup.
	t.Run("the tag it carries is the one the mirror is addressed by", func(t *testing.T) {
		r := runMirrorInstall(t, mirrorOpts{
			oldCurl:       true,
			versionJSON:   "flaky",
			versionFlakyN: 2,
			primary:       "fail",
			mirror:        "ok",
			sums:          "ok",
			latest:        "ok",
		})

		if r.code != 0 {
			t.Fatalf("installer exited %d with a healthy mirror and a recoverable version.json\n%s",
				r.code, r.combined)
		}
		if got := readMirrorFile(t, r.vcPath); got != mirrorMirrorBytes {
			t.Errorf("vc was not installed from the mirror: got %.32q…", got)
		}

		bins := strings.Join(mirrorBinCalls(r.log), "\n")
		if len(bins) == 0 {
			t.Fatalf("no release asset was downloaded\nlog:\n%s", strings.Join(r.log, "\n"))
		}
		if !strings.Contains(bins, "/releases/download/"+mirrorFixtureTag+"/") {
			t.Errorf("the mirror was not addressed by the tag version.json carried (%s):\n%s",
				mirrorFixtureTag, bins)
		}
		if strings.Contains(bins, mirrorLatestTag) {
			t.Errorf("the mirror was addressed by the latest-release tag %s — version.json's own tag was lost to the flapping:\n%s",
				mirrorLatestTag, bins)
		}
		// Same fact from the other side: a version.json that eventually
		// answered leaves the latest-release lookup with no reason to run.
		if calls := mirrorLatestCalls(r.log); len(calls) != 0 {
			t.Errorf("GitHub was asked for the latest release though version.json did answer:\n%s",
				strings.Join(calls, "\n"))
		}
	})
}

// ── 5. VC_SKIP_DOWNLOAD ──────────────────────────────────────────────────────
//
// The flag is used as a fixture elsewhere in this package
// (installer_contract_test.go, to reach the Pi bootstrap without a download),
// which means its meaning is relied upon and asserted nowhere: a run that
// quietly downloaded anyway would still make that test pass. What the flag
// promises is two things, and the second is the one with teeth — it edits the
// user's shell rc file, and a test fixture must not.

func TestShellInstallerSkipDownloadFetchesNothingAndEditsNoRCFile(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the shell installer with command fixtures")
	}

	t.Run("no binary from either source, and an existing rc file is untouched", func(t *testing.T) {
		rc := "# my own zshrc\nexport EDITOR=vi\n"
		// Both sources healthy: nothing but the flag can be what stops the
		// download, so a pass here cannot be a dead fixture in disguise.
		r := runMirrorInstall(t, mirrorOpts{
			primary:      "ok",
			mirror:       "ok",
			sums:         "ok",
			skipDownload: true,
			existingRC:   rc,
		})

		if r.code != 0 {
			t.Fatalf("installer exited %d with VC_SKIP_DOWNLOAD=1\n%s", r.code, r.combined)
		}
		// The run really did get far enough to have downloaded something.
		if len(mirrorVersionJSONCalls(r.log)) == 0 {
			t.Fatalf("the run never reached version.json, so it never reached the download either\nlog:\n%s",
				strings.Join(r.log, "\n"))
		}

		if calls := mirrorPrimaryBinCalls(r.log); len(calls) != 0 {
			t.Errorf("the vc binary was downloaded from the primary despite VC_SKIP_DOWNLOAD=1:\n%s",
				strings.Join(calls, "\n"))
		}
		if calls := append(mirrorBinCalls(r.log), mirrorSumCalls(r.log)...); len(calls) != 0 {
			t.Errorf("the mirror was contacted despite VC_SKIP_DOWNLOAD=1:\n%s", strings.Join(calls, "\n"))
		}
		if _, err := os.Stat(r.vcPath); err == nil {
			t.Errorf("a vc was installed despite VC_SKIP_DOWNLOAD=1: %s holds %.32q…",
				r.vcPath, readMirrorFile(t, r.vcPath))
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat %s: %v", r.vcPath, err)
		}

		// Untouched, not merely "not created": a run that appends a PATH line
		// to the rc file it found is exactly what this flag exists to avoid,
		// and it leaves the file present either way.
		if got := readMirrorFile(t, r.rcPath); got != rc {
			t.Errorf("the user's rc file was edited despite VC_SKIP_DOWNLOAD=1:\nwant %q\ngot  %q", rc, got)
		}
	})

	t.Run("no rc file is created where there was none", func(t *testing.T) {
		r := runMirrorInstall(t, mirrorOpts{primary: "ok", mirror: "ok", sums: "ok", skipDownload: true})

		if r.code != 0 {
			t.Fatalf("installer exited %d with VC_SKIP_DOWNLOAD=1\n%s", r.code, r.combined)
		}
		if info, err := os.Stat(r.rcPath); err == nil {
			t.Errorf("an rc file was created at %s (%d bytes) despite VC_SKIP_DOWNLOAD=1",
				r.rcPath, info.Size())
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat %s: %v", r.rcPath, err)
		}
	})
}
