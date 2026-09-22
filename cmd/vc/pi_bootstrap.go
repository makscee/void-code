package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/config"
	"github.com/spf13/cobra"
)

type piBootstrap struct {
	Version         int                   `json:"version"`
	RelayURL        string                `json:"relayUrl"`
	AuthToken       string                `json:"authToken"`
	PreferredModel  string                `json:"preferredModel,omitempty"`
	Providers       []piBootstrapProvider `json:"providers"`
	SettingsWarning string                `json:"-"`
}
type piBootstrapProvider struct {
	Kind            string   `json:"kind"`
	RelayProviderID string   `json:"relayProviderId"`
	Models          []string `json:"models"`
}

var piBootstrapCmd = &cobra.Command{Use: "pi-bootstrap", Short: "Return transient subscription transport data to the managed Pi extension", Hidden: true, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
	bootstrap, err := currentPiBootstrap()
	if err != nil {
		return err
	}
	if bootstrap.SettingsWarning != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "vc: warning: Pi default model was not reconciled: %s\n", bootstrap.SettingsWarning)
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(bootstrap)
}}

func init() { rootCmd.AddCommand(piBootstrapCmd) }

// currentPiBootstrap exposes every subscription-granted Pi transport to Pi,
// not a VC-selected active provider. Pi's native model picker owns selection.
func currentPiBootstrap() (piBootstrap, error) {
	token, _, err := auth.Load()
	if err != nil || strings.TrimSpace(token) == "" {
		return piBootstrap{}, fmt.Errorf("Pi bootstrap requires `vc login`")
	}
	cfg := config.OSResolve()
	infos, err := fetchProvidersLive(cfg.AuthHost, token, &http.Client{Timeout: authProbeTimeout})
	if err != nil {
		return piBootstrap{}, fmt.Errorf("refresh subscription grants: %w", err)
	}
	preferredModel, settingsErr := reconcilePiBootstrapRetiredDefault()
	out := piBootstrap{
		Version:        1,
		RelayURL:       fmt.Sprintf("%s://%s", cfg.RelayScheme, cfg.RelayHost),
		AuthToken:      token,
		PreferredModel: preferredModel,
		Providers:      make([]piBootstrapProvider, 0),
	}
	if settingsErr != nil {
		out.SettingsWarning = settingsErr.Error()
	}
	for _, info := range infos {
		if strings.EqualFold(strings.TrimSpace(info.Type), "openai-codex-oauth") {
			out.Providers = append(out.Providers, piBootstrapProvider{Kind: "codex", RelayProviderID: info.ID, Models: append([]string(nil), piVoidCodexModels...)})
		}
	}
	return out, nil
}

// A directly loaded managed extension invokes only pi-bootstrap; its parent Pi
// has already cached settings before this process can update them. Return the
// exact successor as a one-startup ordering hint so Pi cannot fall back to Sol
// while the locked writer persists the same migration for later launches.
func reconcilePiBootstrapRetiredDefault() (string, error) {
	var successor string
	err := updatePiSettings(func(settings map[string]any) bool {
		provider, _ := settings["defaultProvider"].(string)
		model, _ := settings["defaultModel"].(string)
		if strings.TrimSpace(provider) != piDefaultProvider {
			return false
		}
		successor = piModelRetirements[strings.TrimSpace(model)]
		if successor == "" {
			return false
		}
		settings["defaultModel"] = successor
		return true
	})
	return successor, err
}
