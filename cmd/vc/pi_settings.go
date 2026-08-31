package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/makscee/void-code/internal/config"
)

type piSettings struct {
	DefaultModel string `json:"defaultModel"`
}

func resolvePiManagedModel(fallback string, supportedModels []string) string {
	path := piSettingsPath()
	if path == "" {
		return fallback
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fallback
	}
	var settings piSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return fallback
	}
	model := strings.TrimSpace(settings.DefaultModel)
	for _, supported := range supportedModels {
		if model == supported {
			return model
		}
	}
	return fallback
}

func piSettingsPath() string {
	if dir := os.Getenv("PI_CODING_AGENT_DIR"); dir != "" {
		return filepath.Join(dir, "settings.json")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".pi", "agent", "settings.json")
}

// The pair vc seeds into Pi's settings so a fresh install opens on the relay's
// own provider and model instead of whichever one Pi happens to register first.
// Both values must stay in step with the extension in pi_extension.go
// (CODEX_PROVIDER_ID / CODEX_MODEL_ID).
const (
	piDefaultProvider = "void-codex"
	piDefaultModel    = "gpt-5.6-terra"
)

// ensurePiDefaultModel seeds defaultModel (and defaultProvider alongside it,
// when the user has not picked one) into Pi's settings.json.
//
// It is a seed, not a policy: an existing defaultModel — any value, from any
// provider — ends the call without touching the file, so a user's choice is
// never taken back. Neither does it invent a pair no provider can serve: a
// user who chose some other provider and no model gets nothing, because the
// model vc would append is one that provider's branch of the extension filters
// out (pi_extension.go). Only vc's own provider, or a file that names no
// provider at all, gets the model seeded.
//
// The file itself belongs to updatePiSettings, which is where the lock, the
// read and the atomic write live.
func ensurePiDefaultModel() error {
	return updatePiSettings(func(settings map[string]any) bool {
		if isNonEmptyJSONString(settings["defaultModel"]) {
			return false
		}
		provider, chosen := settings["defaultProvider"].(string)
		chosen = chosen && strings.TrimSpace(provider) != ""
		if chosen && strings.TrimSpace(provider) != piDefaultProvider {
			return false
		}
		settings["defaultModel"] = piDefaultModel
		if !chosen {
			settings["defaultProvider"] = piDefaultProvider
		}
		return true
	})
}

// piSettingsLockWait is how long a writer waits for the other one to finish.
//
// The floor is set by the widest critical section anything here has: a slow
// process holding the mutator (the cross-process witness in
// pi_settings_owner_test.go holds it for 600ms). The ceiling is set by the
// opposite promise — a writer that cannot get the lock must give up and let Pi
// start, and it has to do so well inside a test's patience rather than hanging
// a launch. Five seconds is the comfortable middle of that range.
const piSettingsLockWait = 5 * time.Second

// piSettingsLockPoll is how often a waiter retries. Short enough that the
// handover after a 600ms hold is not noticeably slower than the hold itself.
const piSettingsLockPoll = 5 * time.Millisecond

// updatePiSettings is the single owner of Pi's settings.json.
//
// It takes a cross-process lock, reads the file (numbers as the digits they
// were written as, never through float64), hands the map to mutate, and writes
// atomically only if mutate returned true. Every writer in vc goes through it,
// so a guarantee one writer keeps — the user's file mode, an integer past
// 2^53, a key some other writer put there — is a guarantee the file has.
//
// The lock is released whatever happens, including an error from the read and
// a panic inside mutate.
func updatePiSettings(mutate func(map[string]any) bool) error {
	path := piSettingsPath()
	if path == "" {
		return errors.New("cannot resolve Pi configuration directory")
	}
	release, err := lockPiSettings(path)
	if err != nil {
		return err
	}
	defer release()

	settings, mode, err := loadPiSettingsMap(path)
	if err != nil {
		return err
	}
	if !mutate(settings) {
		return nil
	}
	return writePiSettingsMap(path, settings, mode)
}

// lockPiSettings takes the lock guarding path and returns the release.
//
// The lock file is NOT in Pi's directory. ~/.pi/agent/ belongs to Pi; vc reads
// and writes one file in there and leaves nothing else behind. It lives in vc's
// own ~/.void-code/, under a name derived from the settings path, so two
// installs pointed at different PI_CODING_AGENT_DIRs serialise against their
// own file instead of against each other.
//
// The file is left in place on release, on purpose. Unlinking a lock file after
// releasing it is the standard way to reintroduce the race it exists to
// prevent: A releases and unlinks while B is still waiting on that inode, C
// creates a fresh file and locks it, and now B and C both believe they hold the
// lock.
func lockPiSettings(settingsPath string) (func(), error) {
	lockPath, err := piSettingsLockPath(settingsPath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(lockPath), 0700); err != nil {
		return nil, fmt.Errorf("prepare Pi settings lock: %w", err)
	}
	file, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, fmt.Errorf("open Pi settings lock: %w", err)
	}
	deadline := time.Now().Add(piSettingsLockWait)
	for {
		locked, lockErr := tryLockFile(file)
		if lockErr != nil {
			file.Close()
			return nil, fmt.Errorf("lock Pi settings: %w", lockErr)
		}
		if locked {
			// Closing the file would release the lock too; unlocking first
			// keeps the two steps honest even if Close is ever dropped.
			return func() {
				_ = unlockFile(file)
				_ = file.Close()
			}, nil
		}
		if !time.Now().Before(deadline) {
			file.Close()
			return nil, fmt.Errorf("another process is writing Pi settings (waited %s for %s)", piSettingsLockWait, lockPath)
		}
		time.Sleep(piSettingsLockPoll)
	}
}

// piSettingsLockPath names the lock after the file it guards, so the name is
// stable across calls (repeated writes reuse one lock file) and distinct across
// Pi directories. The digest keeps a path with separators, spaces or a length
// no filesystem likes from becoming the file name.
func piSettingsLockPath(settingsPath string) (string, error) {
	dir, err := config.CacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve vc cache directory: %w", err)
	}
	key := settingsPath
	if abs, absErr := filepath.Abs(settingsPath); absErr == nil {
		key = abs
	}
	sum := sha256.Sum256([]byte(filepath.Clean(key)))
	return filepath.Join(dir, "pi-settings-"+hex.EncodeToString(sum[:8])+".lock"), nil
}

// loadPiSettingsMap reads settings.json into a map, reporting the mode the file
// must keep. A missing file yields an empty map and 0600 — the mode vc creates
// with; an existing file keeps whatever mode it already had, because this write
// is an update to the user's file and not a re-creation of it.
func loadPiSettingsMap(path string) (map[string]any, fs.FileMode, error) {
	mode := fs.FileMode(0600)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, mode, nil
	}
	if err != nil {
		return nil, mode, fmt.Errorf("read Pi settings: %w", err)
	}
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]any{}, mode, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	// UseNumber keeps every number as the literal it was written as, so a field
	// vc does not understand is written back byte-identical.
	dec.UseNumber()
	var settings map[string]any
	if err := dec.Decode(&settings); err != nil {
		return nil, mode, fmt.Errorf("parse Pi settings without modifying it: %w", err)
	}
	// Anything after the object is content vc cannot represent; writing the map
	// back would drop it silently, so treat the file as malformed instead.
	if _, err := dec.Token(); err != io.EOF {
		return nil, mode, fmt.Errorf("parse Pi settings without modifying it: trailing content after the top-level object")
	}
	if settings == nil {
		settings = map[string]any{}
	}
	return settings, mode, nil
}

func writePiSettingsMap(path string, settings map[string]any, mode fs.FileMode) error {
	next, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	next = append(next, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".settings.json.tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Removed on every path: a failed write must not leave staging next to the
	// file Pi reads.
	defer os.Remove(tmpName)
	if err = tmp.Chmod(mode); err == nil {
		_, err = tmp.Write(next)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// isNonEmptyJSONString reports whether a decoded field holds a user's actual
// choice. A missing key, a null, a blank string or a value of the wrong type
// all count as "not chosen" and may be seeded.
func isNonEmptyJSONString(value any) bool {
	text, ok := value.(string)
	return ok && strings.TrimSpace(text) != ""
}
