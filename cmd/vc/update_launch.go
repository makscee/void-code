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

// launchUpdateCheck fires an async version probe and handles the result before
// the welcome screen renders.  It returns a nudge string to display after the
// banner when the user dismissed an update (pressed 'n').
//
// Side effects:
//   - May install a new binary and exec-restart the process (silent auto-update
//     or 'y'/'a' keypress).
//   - Writes to ~/.void-code/config (prefs update on 'n' and 'a').
//   - Touches ~/.void-code/last-update-check.
func launchUpdateCheck() (nudge string) {
	if checkCacheFresh() {
		return ""
	}

	// Fire async probe.
	probeCh := update.ProbeAsync(version.Version, "", 2*time.Second)

	// Let the probe run; we'll wait below (at most the 2s already in the goroutine).
	result := <-probeCh

	touchUpdateCache()

	if result.Err != nil || !result.HasUpdate {
		return ""
	}

	// We have a newer version.
	latest := result.Latest
	prefs := config.ReadUpdatePrefs()

	// Auto-update path.
	if prefs.AutoUpdate {
		fmt.Printf("\n  ==> auto-updating to %s...\n", latest)
		return runInstallAndRestart(latest)
	}

	// Already declined this exact version.
	if prefs.LastPromptedVersion == latest {
		return fmt.Sprintf("update available · run vc update to install %s", latest)
	}

	// Prompt.
	choice := promptUpdate(version.Version, latest)
	switch choice {
	case updateChoiceYes:
		fmt.Printf("\n  ==> downloading %s...\n", latest)
		return runInstallAndRestart(latest)
	case updateChoiceAlways:
		_ = config.WriteUpdatePrefs(config.UpdatePrefs{
			AutoUpdate:          true,
			LastPromptedVersion: latest,
		})
		fmt.Printf("\n  ==> auto-update enabled. downloading %s...\n", latest)
		return runInstallAndRestart(latest)
	case updateChoiceNo:
		_ = config.WriteUpdatePrefs(config.UpdatePrefs{
			AutoUpdate:          false,
			LastPromptedVersion: latest,
		})
		return fmt.Sprintf("update available · run vc update to install %s", latest)
	}
	return ""
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
