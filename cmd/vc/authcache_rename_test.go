package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// writeAtomicCache replaces a cache file by renaming a finished temp file over
// it. On Windows that rename fails while any other process or goroutine holds
// the target open (a sharing violation): another vc reading the same cache, or
// the reader in TestCodexStartsFromAFreshProvidersCacheAndRefreshesItAfterSpawn,
// which flaked on Windows CI with "the cache was not refreshed … providers hits
// = 1" — the refresh reached auth and a single failed rename threw it away.
//
// Contract pinned here:
//
//	var cacheRename = os.Rename                      // func(oldpath, newpath string) error
//	var cacheRenameRetryDelay time.Duration          // pause between attempts; tests set 0
//
// writeAtomicCache renames through cacheRename, the temp file in the target's
// own directory; a failed rename is tried again, at most 10 attempts in all.
// Success returns true with the new payload in place and no temp file left.
// Giving up returns false, leaves the previous file untouched and removes the
// temp file.

const maxCacheRenameAttempts = 10

type renameProbe struct {
	mu       sync.Mutex
	attempts int
	calls    [][2]string
}

// stubCacheRename makes the first failFirst renames fail (every rename when
// failFirst < 0), then delegates to os.Rename; the retry delay is zero.
func stubCacheRename(t *testing.T, failFirst int) *renameProbe {
	t.Helper()
	probe := &renameProbe{}
	savedRename, savedDelay := cacheRename, cacheRenameRetryDelay
	cacheRename = func(oldpath, newpath string) error {
		probe.mu.Lock()
		probe.attempts++
		n := probe.attempts
		probe.calls = append(probe.calls, [2]string{oldpath, newpath})
		probe.mu.Unlock()
		if failFirst < 0 || n <= failFirst {
			return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: errors.New("The process cannot access the file because it is being used by another process.")}
		}
		return os.Rename(oldpath, newpath)
	}
	cacheRenameRetryDelay = 0
	t.Cleanup(func() { cacheRename, cacheRenameRetryDelay = savedRename, savedDelay })
	return probe
}

func (p *renameProbe) snapshot() (int, [][2]string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.attempts, append([][2]string(nil), p.calls...)
}

// cacheFixture is a cache directory holding one previous cache file.
func cacheFixture(t *testing.T) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, "providers-0123456789abcdef.json")
	if err := os.WriteFile(path, []byte(`{"old":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

func assertOnlyFile(t *testing.T, dir, name string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 1 || names[0] != name {
		t.Errorf("cache directory holds %v, want only [%s] — a temp file was left behind", names, name)
	}
}

func assertRenamesTargetPath(t *testing.T, calls [][2]string, path string) {
	t.Helper()
	for i, c := range calls {
		if c[1] != path {
			t.Errorf("rename %d targets %q, want the cache file %q", i+1, c[1], path)
		}
		if filepath.Dir(c[0]) != filepath.Dir(path) {
			t.Errorf("rename %d moves %q, want a temp file in the cache file's own directory", i+1, c[0])
		}
	}
}

func TestWriteAtomicCacheRetriesATransientRenameFailure(t *testing.T) {
	dir, path := cacheFixture(t)
	probe := stubCacheRename(t, 2)

	if ok := writeAtomicCache(path, []byte(`{"new":true}`)); !ok {
		t.Fatal("writeAtomicCache returned false although the rename succeeded on its third attempt")
	}
	attempts, calls := probe.snapshot()
	if attempts != 3 {
		t.Errorf("rename attempted %d times, want 3 (two failures, then success)", attempts)
	}
	assertRenamesTargetPath(t, calls, path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"new":true}` {
		t.Errorf("cache file = %s, want the new payload", data)
	}
	if info, err := os.Stat(path); err == nil && runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Errorf("cache file mode = %04o, want 0600", info.Mode().Perm())
	}
	assertOnlyFile(t, dir, filepath.Base(path))
}

// The same retry holds for a first write, with no previous file to replace.
func TestWriteAtomicCacheRetriesTheFirstWriteToo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "me-0123456789abcdef.json")
	probe := stubCacheRename(t, 1)

	if ok := writeAtomicCache(path, []byte(`{"new":true}`)); !ok {
		t.Fatal("writeAtomicCache returned false although the rename succeeded on its second attempt")
	}
	if attempts, _ := probe.snapshot(); attempts != 2 {
		t.Errorf("rename attempted %d times, want 2", attempts)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != `{"new":true}` {
		t.Errorf("cache file = %q (%v), want the new payload", data, err)
	}
	assertOnlyFile(t, dir, filepath.Base(path))
}

func TestWriteAtomicCacheGivesUpOnAPersistentRenameFailure(t *testing.T) {
	dir, path := cacheFixture(t)
	probe := stubCacheRename(t, -1)

	started := time.Now()
	ok := writeAtomicCache(path, []byte(`{"new":true}`))
	elapsed := time.Since(started)
	if ok {
		t.Fatal("writeAtomicCache returned true although every rename failed")
	}
	attempts, calls := probe.snapshot()
	if attempts < 2 {
		t.Errorf("rename attempted %d time(s); a failed rename must be tried again", attempts)
	}
	if attempts > maxCacheRenameAttempts {
		t.Errorf("rename attempted %d times, want at most %d", attempts, maxCacheRenameAttempts)
	}
	assertRenamesTargetPath(t, calls, path)
	if elapsed > time.Second {
		t.Errorf("giving up took %s with cacheRenameRetryDelay = 0; the retries must not sleep on their own", elapsed)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"old":true}` {
		t.Errorf("cache file = %s after a failed write, want the previous file untouched", data)
	}
	assertOnlyFile(t, dir, filepath.Base(path))
}
