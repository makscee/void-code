package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/makscee/void-code/internal/config"
	"github.com/makscee/void-code/internal/update"
	"github.com/makscee/void-code/internal/version"
)

const (
	defaultUpdateCheckTTL = time.Hour
	envUpdateCheckTTL     = "VC_UPDATE_CHECK_TTL_S"
)

// updateCheckTTL returns the configured TTL for update-check caches.
// VC_UPDATE_CHECK_TTL_S overrides the default 1h for both vc and cc checks.
func updateCheckTTL() time.Duration {
	if s := os.Getenv(envUpdateCheckTTL); s != "" {
		if secs, err := strconv.Atoi(s); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return defaultUpdateCheckTTL
}

// checkCacheFresh returns true if the update-check cache sentinel was touched
// within the TTL window, meaning we should skip this probe.
func checkCacheFresh() bool {
	path, err := config.UpdateCacheFilePath()
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) < updateCheckTTL()
}

// touchUpdateCache updates the mtime of the update-check sentinel file.
func touchUpdateCache() {
	path, err := config.UpdateCacheFilePath()
	if err != nil {
		return
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	_ = f.Close()
	_ = os.Chtimes(path, time.Now(), time.Now())
}

// launchUpdateCheck performs only the terminal-inert part of launch update
// handling. It may probe the network and touch the check cache, but it never
// reads stdin, writes to the terminal, installs, or restarts while the welcome screen
// owns the terminal. A completed probe can be surfaced as a nonblocking nudge;
// installation remains available through the explicit `vc update` command.
func launchUpdateCheck() string {
	if checkCacheFresh() {
		return ""
	}

	result := <-update.ProbeAsync(version.Version, "", 2*time.Second)
	touchUpdateCache()
	return launchUpdateNudge(result)
}

func launchUpdateNudge(result update.ProbeResult) string {
	if result.Err != nil || !result.HasUpdate {
		return ""
	}
	return fmt.Sprintf("update available · run vc update to install %s", result.Latest)
}
