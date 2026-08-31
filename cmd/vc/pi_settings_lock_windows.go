//go:build windows

package main

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockedByteRange is the region of the lock file LockFileEx is asked about.
// Its contents are never read; one byte is enough to make the range unique,
// and locking a fixed range keeps every holder talking about the same thing.
const lockedByteRange = 1

// tryLockFile takes an exclusive lock on f and reports whether it got it.
// LOCKFILE_FAIL_IMMEDIATELY is what makes it a try rather than a wait — the
// waiting is updatePiSettings's business.
func tryLockFile(f *os.File) (bool, error) {
	overlapped := new(windows.Overlapped)
	err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, lockedByteRange, 0, overlapped,
	)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
		return false, nil
	}
	return false, err
}

func unlockFile(f *os.File) error {
	overlapped := new(windows.Overlapped)
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, lockedByteRange, 0, overlapped)
}
