//go:build !windows

package main

import (
	"errors"
	"os"
	"syscall"
)

// tryLockFile takes an exclusive advisory lock on f and reports whether it got
// it. It never blocks: the waiting is updatePiSettings's business, because a
// blocking flock cannot be given a deadline portably.
//
// The lock is held by the open file description, so it is released by
// unlockFile and also by closing f — which is what makes a process that
// crashes mid-mutation stop blocking everyone else.
func tryLockFile(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return true, nil
	}
	// EWOULDBLOCK and EAGAIN are the same number on every platform we build
	// for; both spellings are matched anyway so this does not depend on that.
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return false, nil
	}
	return false, err
}

func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
