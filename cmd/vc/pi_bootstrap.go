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
	// ModelInfo carries the catalog's labels, in the same order as Models
	// (default first). An older extension ignores it and reads Models alone.
	ModelInfo []piBootstrapModel `json:"modelInfo,omitempty"`
}
type piBootstrapModel struct {
	ID      string `json:"id"`
	Label   string `json:"label,omitempty"`
	Default bool   `json:"default,omitempty"`
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
	out := piBootstrap{
		Version:   1,
		RelayURL:  fmt.Sprintf("%s://%s", cfg.RelayScheme, cfg.RelayHost),
		AuthToken: token,
		Providers: make([]piBootstrapProvider, 0),
	}
	for _, info := range infos {
		if !isCodexProvider(info) {
			continue
		}
		// The provider's own catalog decides the picker; the built-in list is
		// only for a server that predates the catalog.
		catalog, ok := catalogFromModels(info.Models)
		if !ok {
			catalog = builtinPiModelCatalog()
		}
		entry := piBootstrapProvider{Kind: "codex", RelayProviderID: info.ID, Models: catalog.ids()}
		for _, m := range catalog.Models {
			entry.ModelInfo = append(entry.ModelInfo, piBootstrapModel{ID: m.ID, Label: m.Label, Default: m.ID == catalog.Default})
		}
		out.Providers = append(out.Providers, entry)
	}
	return out, nil
}
