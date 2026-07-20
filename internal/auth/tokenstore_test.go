package auth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoadWipeAndPermissions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := Save("opaque-first"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".void-code", "token")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credential mode=%o", info.Mode().Perm())
	}
	dirInfo, _ := os.Stat(filepath.Dir(path))
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode=%o", dirInfo.Mode().Perm())
	}
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
	home := t.TempDir()
	t.Setenv("HOME", home)
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
			home := t.TempDir()
			t.Setenv("HOME", home)
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
	home := t.TempDir()
	t.Setenv("HOME", home)
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
	home := t.TempDir()
	t.Setenv("HOME", home)
	legacy := filepath.Join(home, ".claudev", "token")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("legacy-input-only"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !LegacyTokenExists() {
		t.Fatal("legacy migration input not detected")
	}
	if _, _, err := Load(); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("legacy token was accepted: %v", err)
	}
	if _, err := LoadAndMigrate(); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("legacy token was migrated early: %v", err)
	}
	if err := Wipe(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(legacy)
	if err != nil || string(data) != "legacy-input-only" {
		t.Fatal("legacy input changed")
	}
}
