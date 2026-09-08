//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || zos

package pibin

import "golang.org/x/sys/unix"

// nodeIsExecutable asks the OS whether this process can execute path. Mode bits
// alone are insufficient: the owner, group, and effective credentials decide
// which execute bit applies.
func nodeIsExecutable(path string) bool {
	return unix.Faccessat(unix.AT_FDCWD, path, unix.X_OK, unix.AT_EACCESS) == nil
}
