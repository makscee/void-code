package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
	// The same providers cache a Codex start reads, on the same terms: fresh
	// and holding the ChatGPT grant, or auth is asked and the answer kept.
	infos, ok := readFreshProviders(token, time.Now(), codexGrantType)
	if !ok {
		infos, err = fetchAndCacheProviders(context.Background(), cfg.AuthHost, token, newLaunchHTTPClient())
		if err != nil {
			return piBootstrap{}, fmt.Errorf("refresh subscription grants: %w", err)
		}
	}
	out := piBootstrap{
		Version:   1,
		RelayURL:  fmt.Sprintf("%s://%s", cfg.RelayScheme, cfg.RelayHost),
		AuthToken: token,
		Providers: make([]piBootstrapProvider, 0),
	}
	for _, info := range infos {
		if strings.EqualFold(strings.TrimSpace(info.Type), codexGrantType) {
			out.Providers = append(out.Providers, piBootstrapProvider{Kind: "codex", RelayProviderID: info.ID, Models: append([]string(nil), piVoidCodexModels...)})
		}
	}
	return out, nil
}
