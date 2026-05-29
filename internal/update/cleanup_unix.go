//go:build !windows

package update

// CleanOldBinary is a no-op on non-Windows platforms.
// On Windows it removes the .old binary left over from a previous self-update.
func CleanOldBinary() {}
