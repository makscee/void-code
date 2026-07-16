package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/compat"
	"github.com/makscee/void-code/internal/config"
	"github.com/makscee/void-code/internal/provider"
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

var piBootstrapCmd = &cobra.Command{
	Use:    "pi-bootstrap",
	Short:  "Return transient bootstrap data to the managed Pi extension",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		bootstrap, err := currentPiBootstrap()
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(bootstrap)
	},
}

func init() {
	rootCmd.AddCommand(piBootstrapCmd)
}

func currentPiBootstrap() (piBootstrap, error) {
	token, err := auth.LoadAndMigrate()
	if err != nil || strings.TrimSpace(token) == "" {
		return piBootstrap{}, fmt.Errorf("Pi provider bootstrap requires `vc login`")
	}
	cfg := config.OSResolve()
	infos, err := auth.FetchProviders(cfg.AuthHost, token, &http.Client{Timeout: 10 * time.Second})
	if err != nil {
		return piBootstrap{}, fmt.Errorf("refresh Pi provider grants: %w", err)
	}
	grants := make([]compat.Grant, 0, len(infos))
	for _, info := range infos {
		grants = append(grants, compat.Grant{ID: info.ID, Name: info.Name, Type: info.Type})
	}
	active := provider.Load()
	class := compat.ClassifyProvider(active, provider.LoadLabel(), grants)
	out := piBootstrap{
		Version:   1,
		RelayURL:  fmt.Sprintf("%s://%s", cfg.RelayScheme, cfg.RelayHost),
		AuthToken: token,
	}
	switch class {
	case compat.ProviderChatGPT:
		// RelayProvider must still be present in the current grant response.
		for _, grant := range grants {
			if active.Kind == provider.RelayProvider && grant.ID == active.ID {
				out.Providers = append(out.Providers, piBootstrapProvider{Kind: "codex", RelayProviderID: active.ID, Models: append([]string(nil), piVoidCodexModels...)})
				break
			}
		}
	case compat.ProviderDeepSeek:
		out.Providers = append(out.Providers, piBootstrapProvider{Kind: "deepseek", RelayProviderID: "deepseek", Models: append([]string(nil), piVoidDeepSeekModels...)})
	}
	if len(out.Providers) == 0 {
		return piBootstrap{}, fmt.Errorf("active provider is not in the current compatible grants")
	}
	return out, nil
}
