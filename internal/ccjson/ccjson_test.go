package ccjson_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/makscee/void-code/internal/ccjson"
)

// windowsCredentialPermGap stands in for the 0600 assertion on Windows, where the
// mode bits do not exist to assert.
//
// It rides on t.Log, and that means it is NOT visible in a green CI run: `go test`
// without -v suppresses the output of passing tests. Claiming otherwise was our own
// unchecked assertion, and it is corrected here rather than papered over by turning
// -v on for the whole suite — a switch that would print thousands of lines so that
// one of them could be this. An operator-visible disclosure, if it is ever wanted,
// wants its own workflow step or $GITHUB_STEP_SUMMARY, not a louder test runner.
//
// Who it is for, then: the reader of this file, and of a -v run someone asks for on
// purpose. Same statement as the KNOWN GAP note on writeAtomic in ccjson.go.
const windowsCredentialPermGap = "KNOWN GAP (windows): the 0600 assertion is not run here — " +
	"~/.claude.json is a credential file and writeAtomic asks for 0600, but Windows has no POSIX " +
	"mode bits (Chmod there only toggles the read-only attribute, and 0600 keeps owner-write, so it " +
	"is a no-op and Stat reports 0666). The file is left with the NTFS ACL inherited from the user profile " +
	"directory, which on a stock profile admits only SYSTEM, Administrators and the owner — close to 0600 in " +
	"practice. What is absent is not the protection but the assertion of it: nothing here checks that the " +
	"inheritance is the stock one. See writeAtomic in ccjson.go."

// assertCredentialFilePerm asserts that the written credential file is
// readable and writable by its owner only.
//
// On POSIX that is the literal mode check the package promises. On Windows the
// property does not exist to be checked — see windowsCredentialPermGap — so the
// gap is reported and only what Windows can still answer is asserted: that the
// path is a regular file that was actually created.
func assertCredentialFilePerm(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if runtime.GOOS == "windows" {
		t.Log(windowsCredentialPermGap)
		if !info.Mode().IsRegular() {
			t.Errorf("mode: want a regular file, got %v", info.Mode())
		}
		return
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("file perm: want 0600, got %04o", perm)
	}
}

// TestEnsureDefaults_Absent verifies that EnsureDefaults writes the full seed
// when ~/.claude.json does not exist.
func TestEnsureDefaults_Absent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".claude.json")

	if err := ccjson.EnsureDefaults(target); err != nil {
		t.Fatalf("EnsureDefaults on absent file returned error: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("file was not created: %v", err)
	}

	var got map[string]interface{}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("written content is not valid JSON: %v\ncontent: %s", err, data)
	}

	if v, ok := got["hasCompletedOnboarding"]; !ok || v != true {
		t.Errorf("hasCompletedOnboarding: want true, got %v", v)
	}
	if v, ok := got["theme"]; !ok || v != "dark" {
		t.Errorf("theme: want \"dark\", got %v", v)
	}

	// Verify mode is 0600 (POSIX only — see assertCredentialFilePerm).
	assertCredentialFilePerm(t, target)
}

// TestEnsureDefaults_PartialFalse verifies that a file with
// hasCompletedOnboarding:false gets it flipped to true, and theme is added
// when missing, while all other keys are preserved.
func TestEnsureDefaults_PartialFalse(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".claude.json")

	initial := `{"hasCompletedOnboarding":false,"apiKey":"keep-me"}`
	if err := os.WriteFile(target, []byte(initial), 0600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := ccjson.EnsureDefaults(target); err != nil {
		t.Fatalf("EnsureDefaults returned error: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read after EnsureDefaults: %v", err)
	}

	var got map[string]interface{}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("result is not valid JSON: %v\ncontent: %s", err, data)
	}

	// hasCompletedOnboarding must now be true.
	if v, ok := got["hasCompletedOnboarding"]; !ok || v != true {
		t.Errorf("hasCompletedOnboarding: want true, got %v", v)
	}
	// theme must have been added as "dark".
	if v, ok := got["theme"]; !ok || v != "dark" {
		t.Errorf("theme: want \"dark\" (added), got %v", v)
	}
	// Other keys must be preserved.
	if v, ok := got["apiKey"]; !ok || v != "keep-me" {
		t.Errorf("apiKey: want \"keep-me\" (preserved), got %v", v)
	}
}

// TestEnsureDefaults_MissingTheme verifies that a file missing the theme key
// gets theme:"dark" added while hasCompletedOnboarding and other keys are
// preserved.
func TestEnsureDefaults_MissingTheme(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".claude.json")

	initial := `{"hasCompletedOnboarding":true,"extra":42}`
	if err := os.WriteFile(target, []byte(initial), 0600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := ccjson.EnsureDefaults(target); err != nil {
		t.Fatalf("EnsureDefaults returned error: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read after EnsureDefaults: %v", err)
	}

	var got map[string]interface{}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("result is not valid JSON: %v\ncontent: %s", err, data)
	}

	if v, ok := got["hasCompletedOnboarding"]; !ok || v != true {
		t.Errorf("hasCompletedOnboarding: want true, got %v", v)
	}
	if v, ok := got["theme"]; !ok || v != "dark" {
		t.Errorf("theme: want \"dark\" (added), got %v", v)
	}
	// numeric value preserved as float64 via JSON round-trip.
	if v, ok := got["extra"]; !ok || v != float64(42) {
		t.Errorf("extra: want 42 (preserved), got %v", v)
	}
}

// TestEnsureDefaults_FullyPresent verifies that a file that already has both
// keys correctly set is NOT rewritten (mtime unchanged).
func TestEnsureDefaults_FullyPresent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".claude.json")

	// theme:"light" should be preserved, hasCompletedOnboarding already true.
	initial := `{"hasCompletedOnboarding":true,"theme":"light","fooBar":99}`
	if err := os.WriteFile(target, []byte(initial), 0600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	info0, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat before: %v", err)
	}

	if err := ccjson.EnsureDefaults(target); err != nil {
		t.Fatalf("EnsureDefaults returned error: %v", err)
	}

	info1, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}

	// No change needed → file must not be rewritten.
	if !info1.ModTime().Equal(info0.ModTime()) {
		t.Errorf("file mtime changed — EnsureDefaults must not rewrite an already-correct file")
	}

	// Content still verbatim.
	data, _ := os.ReadFile(target)
	if string(data) != initial {
		t.Errorf("content mutated:\nwant: %s\n got: %s", initial, data)
	}
}

// TestEnsureDefaults_ExistingThemePreserved verifies that a non-dark theme
// already set in the file is never overwritten.
func TestEnsureDefaults_ExistingThemePreserved(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".claude.json")

	// User chose "light", onboarding false — we must flip onboarding but keep theme.
	initial := `{"hasCompletedOnboarding":false,"theme":"light"}`
	if err := os.WriteFile(target, []byte(initial), 0600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := ccjson.EnsureDefaults(target); err != nil {
		t.Fatalf("EnsureDefaults returned error: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read after EnsureDefaults: %v", err)
	}

	var got map[string]interface{}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("result is not valid JSON: %v\ncontent: %s", err, data)
	}

	if v, ok := got["hasCompletedOnboarding"]; !ok || v != true {
		t.Errorf("hasCompletedOnboarding: want true, got %v", v)
	}
	// User's "light" theme MUST be preserved.
	if v, ok := got["theme"]; !ok || v != "light" {
		t.Errorf("theme: want \"light\" (user choice preserved), got %v", v)
	}
}

// TestEnsureDefaults_Unparseable verifies that a file containing invalid JSON
// is left completely untouched and an error is returned.
func TestEnsureDefaults_Unparseable(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".claude.json")

	garbled := `not json at all {{{`
	if err := os.WriteFile(target, []byte(garbled), 0600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	info0, _ := os.Stat(target)

	err := ccjson.EnsureDefaults(target)
	if err == nil {
		t.Fatal("EnsureDefaults must return an error for unparseable JSON")
	}

	// File must be untouched.
	data, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("read after EnsureDefaults: %v", readErr)
	}
	if string(data) != garbled {
		t.Errorf("file content changed — must not clobber unparseable JSON:\nwant: %s\n got: %s", garbled, data)
	}

	info1, _ := os.Stat(target)
	if !info1.ModTime().Equal(info0.ModTime()) {
		t.Errorf("file mtime changed — must not touch unparseable file")
	}
}

// --- EnsureFolderTrust (folder-trust pre-seed) ----------------------------

// readProjects is a small helper to pull projects.<dir>.hasTrustDialogAccepted.
func trustAccepted(t *testing.T, path, dir string) (bool, bool) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, data)
	}
	projects, _ := obj["projects"].(map[string]interface{})
	if projects == nil {
		return false, false
	}
	entry, _ := projects[dir].(map[string]interface{})
	if entry == nil {
		return false, false
	}
	v, ok := entry["hasTrustDialogAccepted"]
	if !ok {
		return false, false
	}
	return v == true, true
}

// TestEnsureFolderTrust_Absent: on a missing file, writes onboarding seed +
// trust for the dir, mode 0600.
func TestEnsureFolderTrust_Absent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".claude.json")
	proj := "/home/u/new-project"

	if err := ccjson.EnsureFolderTrust(target, proj); err != nil {
		t.Fatalf("EnsureFolderTrust absent: %v", err)
	}
	if ok, present := trustAccepted(t, target, proj); !present || !ok {
		t.Errorf("trust for %s: want true, got present=%v ok=%v", proj, present, ok)
	}
	// onboarding still seeded so a standalone call skips first-run
	data, _ := os.ReadFile(target)
	var obj map[string]interface{}
	_ = json.Unmarshal(data, &obj)
	if obj["hasCompletedOnboarding"] != true {
		t.Errorf("hasCompletedOnboarding: want true, got %v", obj["hasCompletedOnboarding"])
	}
	assertCredentialFilePerm(t, target)
}

// TestEnsureFolderTrust_PreservesExisting: merges trust without clobbering
// existing projects entries or other top-level keys.
func TestEnsureFolderTrust_PreservesExisting(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".claude.json")
	initial := `{"hasCompletedOnboarding":true,"apiKey":"keep-me","projects":{"/old":{"hasTrustDialogAccepted":true,"history":["x"]}}}`
	if err := os.WriteFile(target, []byte(initial), 0600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	proj := "/home/u/fresh"
	if err := ccjson.EnsureFolderTrust(target, proj); err != nil {
		t.Fatalf("EnsureFolderTrust: %v", err)
	}
	if ok, _ := trustAccepted(t, target, proj); !ok {
		t.Errorf("new project not trusted")
	}
	// old project + its nested data preserved
	data, _ := os.ReadFile(target)
	var obj map[string]interface{}
	_ = json.Unmarshal(data, &obj)
	if obj["apiKey"] != "keep-me" {
		t.Errorf("apiKey clobbered: %v", obj["apiKey"])
	}
	projects := obj["projects"].(map[string]interface{})
	old := projects["/old"].(map[string]interface{})
	if old["hasTrustDialogAccepted"] != true || old["history"] == nil {
		t.Errorf("/old entry clobbered: %v", old)
	}
}

// TestEnsureFolderTrust_Idempotent: a second identical call performs no write
// (mtime unchanged).
func TestEnsureFolderTrust_Idempotent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".claude.json")
	proj := "/home/u/p"
	if err := ccjson.EnsureFolderTrust(target, proj); err != nil {
		t.Fatalf("first call: %v", err)
	}
	st1, _ := os.Stat(target)
	if err := ccjson.EnsureFolderTrust(target, proj); err != nil {
		t.Fatalf("second call: %v", err)
	}
	st2, _ := os.Stat(target)
	if !st1.ModTime().Equal(st2.ModTime()) {
		t.Errorf("idempotent call rewrote the file (mtime changed)")
	}
}

// TestEnsureFolderTrust_InvalidJSON: never clobbers an unparseable file.
func TestEnsureFolderTrust_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".claude.json")
	bad := `{not json`
	if err := os.WriteFile(target, []byte(bad), 0600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := ccjson.EnsureFolderTrust(target, "/x"); err == nil {
		t.Errorf("expected error on invalid JSON, got nil")
	}
	data, _ := os.ReadFile(target)
	if string(data) != bad {
		t.Errorf("invalid file was modified: %s", data)
	}
}

// TestTrustKeys: backslash dir yields both variants; clean dir yields one;
// empty yields nil.
func TestTrustKeys(t *testing.T) {
	if got := ccjson.TrustKeys(""); got != nil {
		t.Errorf("empty: want nil, got %v", got)
	}
	if got := ccjson.TrustKeys("/home/u/p"); len(got) != 1 || got[0] != "/home/u/p" {
		t.Errorf("unix path: want one entry, got %v", got)
	}
	got := ccjson.TrustKeys(`C:\Users\u\p`)
	if len(got) != 2 || got[0] != `C:\Users\u\p` || got[1] != "C:/Users/u/p" {
		t.Errorf("windows path: want backslash+slash variants, got %v", got)
	}
}
