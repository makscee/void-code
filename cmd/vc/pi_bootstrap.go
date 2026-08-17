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
	Version   int                   `json:"version"`
	RelayURL  string                `json:"relayUrl"`
	AuthToken string                `json:"authToken"`
	Providers []piBootstrapProvider `json:"providers"`
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
	out := piBootstrap{Version: 1, RelayURL: fmt.Sprintf("%s://%s", cfg.RelayScheme, cfg.RelayHost), AuthToken: token}
	for _, info := range infos {
		switch strings.ToLower(strings.TrimSpace(info.Type)) {
		case "openai-codex-oauth":
			out.Providers = append(out.Providers, piBootstrapProvider{Kind: "codex", RelayProviderID: info.ID, Models: append([]string(nil), piVoidCodexModels...)})
		case "deepseek":
			out.Providers = append(out.Providers, piBootstrapProvider{Kind: "deepseek", RelayProviderID: info.ID, Models: append([]string(nil), piVoidDeepSeekModels...)})
		}
	}
	if len(out.Providers) == 0 {
		return piBootstrap{}, fmt.Errorf("no supported Pi transport in current subscription")
	}
	return out, nil
}
