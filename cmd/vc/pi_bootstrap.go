package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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
	// V2 is emitted only with a complete descriptor; the extension fails closed otherwise.
	ModelDecision *piModelDecisionDescriptor `json:"modelDecision,omitempty"`
}

type piModelDecisionDescriptor struct {
	SchemaVersion             int    `json:"schemaVersion"`
	ReadbackURL               string `json:"readbackUrl"`
	PollIntervalSeconds       string `json:"pollIntervalSeconds"`
	CatalogDecisionTTLSeconds string `json:"catalogDecisionTtlSeconds"`
	CatalogExpirySkewSeconds  string `json:"catalogExpirySkewSeconds"`
}

const (
	piModelDecisionPollIntervalSeconds       = "17"
	piModelDecisionCatalogDecisionTTLSeconds = "120"
	piModelDecisionCatalogExpirySkewSeconds  = "2"
)

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
	readback, err := url.Parse(cfg.AccessCheckHost)
	if err != nil || strings.TrimSpace(cfg.AccessCheckHost) != cfg.AccessCheckHost ||
		(readback.Scheme != "http" && readback.Scheme != "https") || readback.Host == "" ||
		readback.User != nil || readback.Path != "" || readback.RawQuery != "" || readback.Fragment != "" {
		return piBootstrap{}, fmt.Errorf("configured model-decision readback authority is invalid")
	}
	infos, err := fetchProvidersLive(cfg.AuthHost, token, &http.Client{Timeout: authProbeTimeout})
	if err != nil {
		return piBootstrap{}, fmt.Errorf("refresh subscription grants: %w", err)
	}
	out := piBootstrap{
		Version:   2,
		RelayURL:  fmt.Sprintf("%s://%s", cfg.RelayScheme, cfg.RelayHost),
		AuthToken: token,
		Providers: make([]piBootstrapProvider, 0),
		ModelDecision: &piModelDecisionDescriptor{
			SchemaVersion:             1,
			ReadbackURL:               cfg.AccessCheckHost + "/v1/vc/me",
			PollIntervalSeconds:       piModelDecisionPollIntervalSeconds,
			CatalogDecisionTTLSeconds: piModelDecisionCatalogDecisionTTLSeconds,
			CatalogExpirySkewSeconds:  piModelDecisionCatalogExpirySkewSeconds,
		},
	}
	for _, info := range infos {
		if strings.EqualFold(strings.TrimSpace(info.Type), "openai-codex-oauth") {
			out.Providers = append(out.Providers, piBootstrapProvider{Kind: "codex", RelayProviderID: info.ID, Models: append([]string(nil), piVoidCodexModels...)})
		}
	}
	return out, nil
}
