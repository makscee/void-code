package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type managedWebSearchState string

const (
	managedWebSearchReady       managedWebSearchState = "installed"
	managedWebSearchUnavailable managedWebSearchState = "unavailable"
	managedWebSearchBroken      managedWebSearchState = "broken"
	// managedWebSearchPending: eligible and owned, but not installed or not
	// current; a session installs it in the background (see
	// startManagedWebSearchInstall).
	managedWebSearchPending        managedWebSearchState = "pending"
	managedWebSearchPackageName                          = "@void-code/pi-web-access"
	managedWebSearchMarker                               = "VC-10 managed void-codex seam v1"
	managedWebSearchPackageVersion                       = "0.13.0-void.6"
)

var renameManagedWebSearchPath = os.Rename

// managedWebSearchInstallGrace is how long vc waits, after Pi exits, for a
// web-search install still in flight before it cancels it. A product decision
// (Artem, void-works#89): long enough for a typical npm ci to land after a
// short session, short enough that quitting Pi does not feel like vc hung. An
// install cut short is retried by the next launch.
var managedWebSearchInstallGrace = 15 * time.Second

// staleWebSearchStagingAge is how old a .pi-web-access-stage-* or
// .pi-web-access-backup-* sibling must be before an install sweeps it. No real
// install takes that long, so a second vc installing at the same moment never
// loses its live stage.
var staleWebSearchStagingAge = 30 * time.Minute

const (
	webSearchStagePrefix  = ".pi-web-access-stage-"
	webSearchBackupPrefix = ".pi-web-access-backup-"
)

func managedWebSearchPackagePath() string {
	// Keep the original managed slot so upgrades replace it in place instead of
	// registering two copies of the same Pi extension.
	return filepath.Join(piAgentDir(), "void-code", "pi-web-access-0.13.0-void.1")
}

// reconcileManagedWebSearch brings the managed web-search package to the state
// the session asks for, installing it in the foreground when needed.
func reconcileManagedWebSearch(eligible bool) (managedWebSearchState, error) {
	state, err := checkManagedWebSearch(eligible)
	if err != nil || state != managedWebSearchPending {
		return state, err
	}
	path := managedWebSearchPackagePath()
	if err := installManagedWebSearchPackage(context.Background(), path); err != nil {
		return managedWebSearchBroken, err
	}
	if err := reconcileManagedPackageSetting(path, true); err != nil {
		return managedWebSearchBroken, err
	}
	return managedWebSearchReady, nil
}

// prepareSessionWebSearch is what a Pi launch does about web search before Pi
// starts: everything reconcileManagedWebSearch does except the install, which
// the session runs alongside Pi (managedWebSearchPending). While it is pending,
// settings.json never points Pi at a package that is not there; an older,
// owned copy that is there stays as it is until a complete one replaces it.
func prepareSessionWebSearch(eligible bool) (managedWebSearchState, error) {
	state, err := checkManagedWebSearch(eligible)
	if err != nil || state != managedWebSearchPending {
		return state, err
	}
	path := managedWebSearchPackagePath()
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		if err := reconcileManagedPackageSetting(path, false); err != nil {
			return managedWebSearchBroken, err
		}
	}
	return managedWebSearchPending, nil
}

// checkManagedWebSearch handles opt-out, ineligibility and ownership, and
// registers a current package. It reports managedWebSearchPending, without
// touching anything, when the package needs installing.
func checkManagedWebSearch(eligible bool) (managedWebSearchState, error) {
	if piAgentDir() == "" {
		return managedWebSearchBroken, fmt.Errorf("cannot resolve Pi configuration directory")
	}
	disabled := isFalse(os.Getenv("VC_PI_MANAGED_WEB_SEARCH"))
	path := managedWebSearchPackagePath()
	if disabled {
		if err := removeManagedWebSearchPackage(path); err != nil {
			return managedWebSearchBroken, err
		}
		if err := reconcileManagedPackageSetting(path, false); err != nil {
			return managedWebSearchBroken, err
		}
		return managedWebSearchUnavailable, nil
	}
	if !eligible {
		// Ordinary ineligibility or unknown authority must not destructively
		// change an installation owned by vc. Explicit opt-out above is the sole
		// deregistration/removal operation.
		return managedWebSearchUnavailable, nil
	}

	current, foreign, err := inspectManagedWebSearchPackage(path)
	if err != nil {
		return managedWebSearchBroken, err
	}
	if foreign {
		return managedWebSearchBroken, fmt.Errorf("managed web-search path %s is not owned by void-code", path)
	}
	if !current {
		return managedWebSearchPending, nil
	}
	if err := reconcileManagedPackageSetting(path, true); err != nil {
		return managedWebSearchBroken, err
	}
	return managedWebSearchReady, nil
}

// installManagedWebSearch is a session's background install: the package is
// staged and swapped in whole. Registering it in settings.json is left to
// registerManagedWebSearch, which runs after Pi has exited, so vc never writes
// Pi's settings while Pi owns them.
func installManagedWebSearch(ctx context.Context) error {
	return installManagedWebSearchPackage(ctx, managedWebSearchPackagePath())
}

func registerManagedWebSearch() error {
	return reconcileManagedPackageSetting(managedWebSearchPackagePath(), true)
}

// backgroundWebSearchInstall is a web-search install running alongside Pi.
// Its result is held, not printed: the terminal belongs to Pi until Pi exits.
type backgroundWebSearchInstall struct {
	cancel   context.CancelFunc
	done     chan struct{}
	err      error
	register func() error
	once     sync.Once
	result   error
}

func startManagedWebSearchInstall(install func(context.Context) error, register func() error) *backgroundWebSearchInstall {
	ctx, cancel := context.WithCancel(context.Background())
	b := &backgroundWebSearchInstall{cancel: cancel, done: make(chan struct{}), register: register}
	go func() {
		defer close(b.done)
		b.err = install(ctx)
	}()
	return b
}

// finish is called once Pi has exited. It waits for the install at most grace,
// then cancels it and waits for it to unwind, so nothing is left publishing
// into the package directory after vc returns. A successful install is then
// registered for the next launch. Safe on nil and safe to call twice.
func (b *backgroundWebSearchInstall) finish(grace time.Duration) error {
	if b == nil {
		return nil
	}
	b.once.Do(func() { b.result = b.wait(grace) })
	return b.result
}

func (b *backgroundWebSearchInstall) wait(grace time.Duration) error {
	defer b.cancel()
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-b.done:
	case <-timer.C:
		b.cancel()
		<-b.done
		if b.err != nil {
			return fmt.Errorf("install did not finish within %s of Pi exiting and was stopped (the next launch retries it): %w", grace, b.err)
		}
	}
	if b.err != nil {
		return b.err
	}
	if b.register == nil {
		return nil
	}
	return b.register()
}

func inspectManagedWebSearchPackage(path string) (current, foreign bool, err error) {
	data, err := os.ReadFile(filepath.Join(path, "package.json"))
	if os.IsNotExist(err) {
		if _, statErr := os.Stat(path); statErr == nil {
			return false, true, nil
		}
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("read managed web-search package: %w", err)
	}
	var pkg struct {
		Name, Version string
		VoidCodeFork  struct{ Patch string } `json:"voidCodeFork"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return false, true, nil
	}
	owned := pkg.Name == managedWebSearchPackageName && pkg.VoidCodeFork.Patch == managedWebSearchMarker
	if !owned {
		return false, true, nil
	}
	_, depErr := os.Stat(filepath.Join(path, "node_modules", "@mozilla", "readability", "package.json"))
	return pkg.Version == managedWebSearchPackageVersion && depErr == nil, false, nil
}

func installManagedWebSearchPackage(ctx context.Context, path string) (installErr error) {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return fmt.Errorf("create managed package parent: %w", err)
	}
	sweepStaleWebSearchStaging(parent)
	stage, err := os.MkdirTemp(parent, webSearchStagePrefix+"*")
	if err != nil {
		return fmt.Errorf("stage managed web-search package: %w", err)
	}
	// A published stage has been renamed away and this is a no-op. A stage
	// that cannot be removed is reported, not swallowed.
	defer func() {
		if removeErr := os.RemoveAll(stage); removeErr != nil {
			installErr = &webSearchStageLeftover{install: installErr, stage: stage, removeErr: removeErr}
		}
	}()
	root := "embed/pi-web-access-0.13.0"
	err = fs.WalkDir(piWebAccessFork, root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, _ := filepath.Rel(root, name)
		if rel == "." {
			return nil
		}
		dst := filepath.Join(stage, rel)
		if entry.IsDir() {
			return os.MkdirAll(dst, 0700)
		}
		data, readErr := piWebAccessFork.ReadFile(name)
		if readErr != nil {
			return readErr
		}
		return os.WriteFile(dst, data, 0600)
	})
	if err != nil {
		return fmt.Errorf("copy managed web-search fork: %w", err)
	}
	if err := installManagedWebSearchDependencies(ctx, stage); err != nil {
		return err
	}
	// A cancelled install publishes nothing, even if npm got to the end.
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, foreign, err := inspectManagedWebSearchPackage(path); err != nil {
		return err
	} else if foreign {
		return fmt.Errorf("managed web-search path %s is not owned by void-code", path)
	}
	backup := ""
	if _, err := os.Stat(path); err == nil {
		backupDir, err := os.MkdirTemp(parent, webSearchBackupPrefix+"*")
		if err != nil {
			return fmt.Errorf("prepare managed web-search rollback: %w", err)
		}
		if err := os.Remove(backupDir); err != nil {
			return fmt.Errorf("prepare managed web-search rollback: %w", err)
		}
		backup = backupDir
		if err := renameManagedWebSearchPath(path, backup); err != nil {
			return fmt.Errorf("stage prior managed web-search package: %w", err)
		}
	}
	if err := renameManagedWebSearchPath(stage, path); err != nil {
		if backup != "" {
			if rollbackErr := renameManagedWebSearchPath(backup, path); rollbackErr != nil {
				return fmt.Errorf("publish managed web-search package: %w (rollback failed: %v)", err, rollbackErr)
			}
		}
		return fmt.Errorf("publish managed web-search package: %w", err)
	}
	if backup != "" {
		_ = os.RemoveAll(backup)
	}
	return nil
}

// webSearchStageLeftover is an install whose staging directory could not be
// removed. Its message is the install's own; the leftover is said separately
// (see finishWebSearch) so it gets its own warning line.
type webSearchStageLeftover struct {
	install   error
	stage     string
	removeErr error
}

func (e *webSearchStageLeftover) Error() string {
	if e.install == nil {
		return e.leftover()
	}
	return e.install.Error()
}

func (e *webSearchStageLeftover) Unwrap() error { return e.install }

func (e *webSearchStageLeftover) leftover() string {
	return fmt.Sprintf("staging directory %s could not be removed: %v", e.stage, e.removeErr)
}

// sweepStaleWebSearchStaging removes stage and backup siblings that a crashed
// or killed earlier run left behind, once they are older than
// staleWebSearchStagingAge. Nothing else in parent is touched. Best effort: a
// sibling that cannot be removed now is tried again by the next install.
func sweepStaleWebSearchStaging(parent string) {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-staleWebSearchStagingAge)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, webSearchStagePrefix) && !strings.HasPrefix(name, webSearchBackupPrefix) {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(parent, name))
	}
}

func removeManagedWebSearchPackage(path string) error {
	_, foreign, err := inspectManagedWebSearchPackage(path)
	if err != nil {
		return err
	}
	if foreign {
		return fmt.Errorf("managed web-search path %s is not owned by void-code", path)
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove managed web-search package: %w", err)
	}
	return nil
}

// reconcileManagedPackageSetting adds or removes the managed package path in
// settings.json's "packages", and does nothing at all when the file already
// says what it should.
//
// It owns one key and nothing else: the read, the lock and the atomic write are
// updatePiSettings's, so a user's file mode and the other writer's keys survive
// a change here the same way they survive a change there. The error a mutator
// cannot return travels out in mutateErr.
func reconcileManagedPackageSetting(packagePath string, present bool) error {
	var mutateErr error
	err := updatePiSettings(func(settings map[string]any) bool {
		raw, exists := settings["packages"]
		packages := []any{}
		if exists {
			var ok bool
			packages, ok = raw.([]any)
			if !ok {
				mutateErr = fmt.Errorf("Pi settings packages is not an array")
				return false
			}
		}
		out := make([]any, 0, len(packages)+1)
		matches := 0
		for _, item := range packages {
			if source, ok := item.(string); ok && source == packagePath {
				matches++
				if !present || matches > 1 {
					continue
				}
			}
			out = append(out, item)
		}
		if present && matches == 0 {
			out = append(out, packagePath)
		}
		if !present && !exists {
			return false
		}
		if (present && matches == 1) || (!present && matches == 0) {
			return false
		}
		settings["packages"] = out
		return true
	})
	if mutateErr != nil {
		return mutateErr
	}
	return err
}

func inspectManagedPackageSetting(packagePath string) (bool, error) {
	data, err := os.ReadFile(piSettingsPath())
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read Pi settings: %w", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return false, fmt.Errorf("parse Pi settings: %w", err)
	}
	raw, ok := settings["packages"]
	if !ok {
		return false, nil
	}
	packages, ok := raw.([]any)
	if !ok {
		return false, fmt.Errorf("Pi settings packages is not an array")
	}
	matches := 0
	for _, item := range packages {
		if source, ok := item.(string); ok && source == packagePath {
			matches++
		}
	}
	return matches == 1, nil
}
