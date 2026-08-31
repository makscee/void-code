package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
// never taken back. Everything else in the file survives verbatim, including
// keys vc knows nothing about; numbers are carried as their on-disk digits
// rather than through float64, which would silently rewrite an integer past
// 2^53. Malformed JSON is reported, never repaired: rewriting a file we could
// not parse would destroy settings we cannot read.
func ensurePiDefaultModel() error {
	path := piSettingsPath()
	if path == "" {
		return errors.New("cannot resolve Pi configuration directory")
	}
	settings, mode, err := loadPiSettingsMap(path)
	if err != nil {
		return err
	}
	if isNonEmptyJSONString(settings["defaultModel"]) {
		return nil
	}
	settings["defaultModel"] = piDefaultModel
	if !isNonEmptyJSONString(settings["defaultProvider"]) {
		settings["defaultProvider"] = piDefaultProvider
	}
	return writePiSettingsMap(path, settings, mode)
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
