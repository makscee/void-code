package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

type piDefaultValueSnapshot struct {
	Present bool            `json:"present"`
	Value   json.RawMessage `json:"value,omitempty"`
}

type piModelDefaultsSnapshot struct {
	Provider piDefaultValueSnapshot `json:"provider"`
	Model    piDefaultValueSnapshot `json:"model"`
}

var piModelDefaultSnapshotCmd = &cobra.Command{
	Use:    "pi-model-default-snapshot",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		snapshot, err := readPiModelDefaultsSnapshot()
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(snapshot)
	},
}

var piModelDefaultRestoreCmd = &cobra.Command{
	Use:    "pi-model-default-restore SUCCESSOR",
	Hidden: true,
	Args:   cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if args[0] != "gpt-6-sol" && args[0] != "gpt-6-luna" {
			return fmt.Errorf("invalid migration successor %q", args[0])
		}
		payload, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 4097))
		if err != nil {
			return err
		}
		if len(payload) > 4096 {
			return fmt.Errorf("model default snapshot is too large")
		}
		var snapshot piModelDefaultsSnapshot
		if err := json.Unmarshal(payload, &snapshot); err != nil {
			return fmt.Errorf("decode model default snapshot: %w", err)
		}
		return restorePiModelDefaults(snapshot, args[0])
	},
}

func init() {
	rootCmd.AddCommand(piModelDefaultSnapshotCmd, piModelDefaultRestoreCmd)
}

func readPiModelDefaultsSnapshot() (piModelDefaultsSnapshot, error) {
	path := piSettingsPath()
	if path == "" {
		return piModelDefaultsSnapshot{}, fmt.Errorf("cannot resolve Pi configuration directory")
	}
	release, err := lockPiSettings(path)
	if err != nil {
		return piModelDefaultsSnapshot{}, err
	}
	defer release()
	settings, _, err := loadPiSettingsMap(path)
	if err != nil {
		return piModelDefaultsSnapshot{}, err
	}
	return snapshotPiModelDefaults(settings)
}

func snapshotPiModelDefaults(settings map[string]any) (piModelDefaultsSnapshot, error) {
	capture := func(key string) (piDefaultValueSnapshot, error) {
		value, present := settings[key]
		if !present {
			return piDefaultValueSnapshot{}, nil
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return piDefaultValueSnapshot{}, fmt.Errorf("encode %s: %w", key, err)
		}
		return piDefaultValueSnapshot{Present: true, Value: raw}, nil
	}
	provider, err := capture("defaultProvider")
	if err != nil {
		return piModelDefaultsSnapshot{}, err
	}
	model, err := capture("defaultModel")
	if err != nil {
		return piModelDefaultsSnapshot{}, err
	}
	return piModelDefaultsSnapshot{Provider: provider, Model: model}, nil
}

func restorePiModelDefaults(snapshot piModelDefaultsSnapshot, successor string) error {
	return updatePiSettings(func(settings map[string]any) bool {
		provider, _ := settings["defaultProvider"].(string)
		model, _ := settings["defaultModel"].(string)
		// Do not overwrite a concurrent user change. pi.setModel writes exactly
		// this pair before this compare-and-restore operation.
		if strings.TrimSpace(provider) != piDefaultProvider || strings.TrimSpace(model) != successor {
			return false
		}
		restorePiDefaultValue(settings, "defaultProvider", snapshot.Provider)
		restorePiDefaultValue(settings, "defaultModel", snapshot.Model)
		// A directly managed launch can reach this helper without vc's normal
		// preflight. Never restore an old VC pair in that case.
		migrateRetiredPiSelection(settings)
		return true
	})
}

func restorePiDefaultValue(settings map[string]any, key string, snapshot piDefaultValueSnapshot) {
	if !snapshot.Present {
		delete(settings, key)
		return
	}
	var value any
	if err := json.Unmarshal(snapshot.Value, &value); err != nil {
		return
	}
	settings[key] = value
}
