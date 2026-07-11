package main

import (
	"encoding/json"
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
