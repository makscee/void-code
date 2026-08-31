package auth

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// withTempHome points the home directory at a throwaway one, for the tests in
// this file — every one of them writes a credential.
//
// Both variables, because os.UserHomeDir does not read the same one everywhere:
// HOME on unix, USERPROFILE on Windows. Setting only HOME left the Windows run
// resolving the real profile, so these five tests overwrote the developer's own
// ~/.void-code/token — silently, since on the platform the author was using it
// worked. One helper rather than five hand-written pairs, so the pair can only
// be forgotten in one place; home_isolation_test.go is what notices when it is.
func withTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

// assertCredentialFileIsPrivate makes the "only this account can read the
// token" claim in the strongest form the running platform can carry.
//
// On POSIX that is the literal 0600. Windows has no permission bits for Go to
// report: os.Stat gives 0666 for any ordinary file and 0444 for a read-only
// one, os.Chmod moves only the read-only flag, and the real access control
// lives in ACLs that os.FileMode cannot express. Asking for 0600 there asks for
// a number the platform will never produce, and the test fails on correct code.
//
// Plainly, so a green Windows run is not read as two platforms agreeing: on
// Windows the privacy of the credential is NOT checked here, and nothing else
// in this package checks it either. That claim is verified on POSIX only. What
// remains on Windows is narrow but real — the credential must come out
// writable, because Save replaces it by renaming over it, and a rename onto a
// read-only file fails there.
func assertCredentialFileIsPrivate(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	got := info.Mode().Perm()
	if runtime.GOOS == "windows" {
		if got&0200 == 0 {
			t.Errorf("credential mode=%04o, want a writable file — a read-only credential cannot be replaced by the next Save", got)
		}
		return
	}
	if got != 0o600 {
		t.Errorf("credential mode=%04o, want 0600", got)
	}
}

// assertCredentialDirIsPrivate makes the same claim about the directory the
// credential lives in: 0700 on POSIX.
//
// On Windows it makes no mode claim at all — os.Stat reports 0777 for every
// directory a test can create, so there is no value to compare against that
// would distinguish a private directory from a world-readable one. What is left
// is that the directory exists and is a directory, which is worth keeping only
// because Save is expected to have created it. The privacy of the credential
// directory is verified on POSIX.
func assertCredentialDirIsPrivate(t *testing.T, dir string) {
	t.Helper()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Fatalf("credential directory %s is not a directory", dir)
	}
	if runtime.GOOS == "windows" {
		return
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf("directory mode=%04o, want 0700", got)
	}
}

func TestSaveLoadWipeAndPermissions(t *testing.T) {
	home := withTempHome(t)
	if err := Save("opaque-first"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".void-code", "token")
	assertCredentialFileIsPrivate(t, path)
	assertCredentialDirIsPrivate(t, filepath.Dir(path))
	got, legacy, err := Load()
	if err != nil || got != "opaque-first" || legacy {
		t.Fatalf("load mismatch")
	}
	if err := Wipe(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("expected logged out, got %v", err)
	}
}

func TestAtomicReplacementPreservesPreviousOnRenameFailure(t *testing.T) {
	withTempHome(t)
	if err := Save("previous-credential"); err != nil {
		t.Fatal(err)
	}
	path, _ := tokenPath()
	failure := errors.New("injected rename failure")
	if err := saveAt(path, "replacement-credential", func(string, string) error { return failure }); !errors.Is(err, failure) {
		t.Fatalf("got %v", err)
	}
	got, _, err := Load()
	if err != nil || got != "previous-credential" {
		t.Fatalf("previous credential was replaced: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".token-*"))
	if len(matches) != 0 {
		t.Fatalf("temporary credentials remain")
	}
	if err := Save("replacement-credential"); err != nil {
		t.Fatal(err)
	}
	got, _, _ = Load()
	if got != "replacement-credential" {
		t.Fatal("replacement did not persist")
	}
}

func TestReplacementFaultsRestorePreviousCredentialAndCleanup(t *testing.T) {
	phases := []string{"write", "file-sync", "rename", "directory-sync", "cleanup-directory-sync"}
	for _, phase := range phases {
		t.Run(phase, func(t *testing.T) {
			withTempHome(t)
			if err := Save("previous-credential"); err != nil {
				t.Fatal(err)
			}
			path, _ := tokenPath()
			ops := defaultCredentialOps()
			failure := errors.New("injected " + phase)
			renames, dirSyncs := 0, 0
			if phase == "write" {
				ops.write = func(*os.File, string) error { return failure }
			}
			if phase == "file-sync" {
				ops.fileSync = func(*os.File) error { return failure }
			}
			if phase == "rename" {
				original := ops.rename
				ops.rename = func(from, to string) error {
					renames++
					if renames == 2 {
						return failure
					}
					return original(from, to)
				}
			}
			if phase == "directory-sync" || phase == "cleanup-directory-sync" {
				original := ops.dirSync
				failAt := 1
				if phase == "cleanup-directory-sync" {
					failAt = 2
				}
				ops.dirSync = func(dir string) error {
					dirSyncs++
					if dirSyncs == failAt {
						return failure
					}
					return original(dir)
				}
			}
			if err := saveWithOps(path, "replacement-credential", ops); !errors.Is(err, failure) {
				t.Fatalf("got %v", err)
			}
			got, _, err := Load()
			if err != nil || got != "previous-credential" {
				t.Fatalf("restart load=%q err=%v", got, err)
			}
			for _, pattern := range []string{".token-*", ".token-backup-*", ".token-rollback-*"} {
				matches, _ := filepath.Glob(filepath.Join(filepath.Dir(path), pattern))
				if len(matches) != 0 {
					t.Fatalf("leftover %s", matches)
				}
			}
		})
	}
}

func TestReplacementSuccessCompletesFileAndDirectorySync(t *testing.T) {
	withTempHome(t)
	if err := Save("previous-credential"); err != nil {
		t.Fatal(err)
	}
	path, _ := tokenPath()
	ops := defaultCredentialOps()
	fileSyncs, dirSyncs := 0, 0
	originalFileSync, originalDirSync := ops.fileSync, ops.dirSync
	ops.fileSync = func(file *os.File) error { fileSyncs++; return originalFileSync(file) }
	ops.dirSync = func(dir string) error { dirSyncs++; return originalDirSync(dir) }
	if err := saveWithOps(path, "replacement-credential", ops); err != nil {
		t.Fatal(err)
	}
	got, _, err := Load()
	if err != nil || got != "replacement-credential" || fileSyncs != 1 || dirSyncs != 2 {
		t.Fatalf("got=%q fileSyncs=%d dirSyncs=%d err=%v", got, fileSyncs, dirSyncs, err)
	}
}

func TestLegacyTokenIsRetainedButNeverLoaded(t *testing.T) {
	home := withTempHome(t)
	legacy := filepath.Join(home, ".claudev", "token")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("legacy-input-only"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("legacy token was accepted: %v", err)
	}
	if err := Wipe(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(legacy)
	if err != nil || string(data) != "legacy-input-only" {
		t.Fatal("legacy input changed")
	}
}
