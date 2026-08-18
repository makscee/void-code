package main

// ensureBypassPermissionMode remains a no-op compatibility helper for unreachable
// legacy code paths while Pi is the sole supported runtime.
func ensureBypassPermissionMode(args []string) []string { return args }
