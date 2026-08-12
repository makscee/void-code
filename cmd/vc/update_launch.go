package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/makscee/void-code/internal/ccupdate"
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
// reads stdin, writes to the terminal, installs, or restarts while welcome.Run
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

// runInstallAndRestart downloads the latest binary, replaces it, and
// exec-restarts.  If any step fails, returns an error nudge string (install
// failed, user can retry with vc update).
func runInstallAndRestart(latest string) string {
	updated, err := update.CheckAndUpdate(update.Options{
		Current: version.Version,
	})
	if err != nil {
		return fmt.Sprintf("update failed: %v — try: vc update", err)
	}
	if !updated {
		return ""
	}
	fmt.Println("  ==> installing...")

	exe, err := os.Executable()
	if err != nil {
		return fmt.Sprintf("restart failed: %v — re-run vc", err)
	}
	fmt.Println("  ==> restarting...")
	if err := update.RestartWithNewBinary(exe); err != nil {
		// Windows: process was spawned and we exit below via os.StartProcess.
		// On unix this is fatal.
		return fmt.Sprintf("restart failed: %v — re-run vc", err)
	}
	// Unreachable on unix (syscall.Exec replaces process).
	return ""
}

// launchCCUpdateCheck checks the installed @anthropic-ai/claude-code version
// against npm registry and installs the latest if stale.  It prints the result
// directly to stdout (e.g. "claude-code: v1.x → v1.y") so the user sees it
// before claude starts.  Called from runSpawn after vc self-update completes.
//
// The check is skipped when the TTL sentinel is fresh.
// Network failures are silent; only hard npm errors surface a one-liner.
func launchCCUpdateCheck() {
	// Wire the cache path so ccupdate uses the same cache dir.
	if ccupdate.CachePath == "" {
		if p, err := config.CCUpdateCacheFilePath(); err == nil {
			ccupdate.CachePath = p
		}
	}

	msg := ccupdate.CheckAndUpdate()
	if msg != "" {
		fmt.Println(msg)
	}
}
