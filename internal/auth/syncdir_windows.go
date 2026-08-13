//go:build windows

package auth

// Windows does not provide the Unix directory fsync durability primitive.
// The credential file itself is still flushed before its atomic rename.
func syncDirectory(string) error { return nil }
