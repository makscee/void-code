package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
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

type piModelDecisionTiming struct {
	pollIntervalSeconds string
	decisionTTLSeconds  string
	expirySkewSeconds   string
}

func parseCanonicalModelDecisionSeconds(name, value string, min, max uint64) (uint64, error) {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return 0, fmt.Errorf("%s must be a canonical unsigned decimal", name)
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return 0, fmt.Errorf("%s must be a canonical unsigned decimal", name)
		}
	}
	n, err := strconv.ParseUint(value, 10, 64)
	if err != nil || n < min || n > max {
		return 0, fmt.Errorf("%s must be in [%d,%d]", name, min, max)
	}
	return n, nil
}

func configuredModelDecisionTiming(cfg config.Config) (piModelDecisionTiming, error) {
	_, err := parseCanonicalModelDecisionSeconds(config.EnvModelDecisionPollIntervalSeconds, cfg.ModelDecisionPollIntervalSeconds, 1, 300)
	if err != nil {
		return piModelDecisionTiming{}, err
	}
	ttl, err := parseCanonicalModelDecisionSeconds(config.EnvModelDecisionTTLSeconds, cfg.ModelDecisionTTLSeconds, 1, 2147483647)
	if err != nil {
		return piModelDecisionTiming{}, err
	}
	skew, err := parseCanonicalModelDecisionSeconds(config.EnvModelDecisionExpirySkewSeconds, cfg.ModelDecisionExpirySkewSeconds, 0, 2147483647)
	if err != nil {
		return piModelDecisionTiming{}, err
	}
	if skew >= ttl {
		return piModelDecisionTiming{}, fmt.Errorf("%s must be less than %s", config.EnvModelDecisionExpirySkewSeconds, config.EnvModelDecisionTTLSeconds)
	}
	return piModelDecisionTiming{
		pollIntervalSeconds: cfg.ModelDecisionPollIntervalSeconds,
		decisionTTLSeconds:  cfg.ModelDecisionTTLSeconds,
		expirySkewSeconds:   cfg.ModelDecisionExpirySkewSeconds,
	}, nil
}

// currentPiBootstrap exposes every subscription-granted Pi transport to Pi,
// not a VC-selected active provider. Pi's native model picker owns selection.
func currentPiBootstrap() (piBootstrap, error) {
	token, _, err := auth.Load()
	if err != nil || strings.TrimSpace(token) == "" {
		return piBootstrap{}, fmt.Errorf("Pi bootstrap requires `vc login`")
	}
	cfg := config.OSResolve()
	timing, err := configuredModelDecisionTiming(cfg)
	if err != nil {
		return piBootstrap{}, fmt.Errorf("invalid model-decision timing configuration: %w", err)
	}
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
			PollIntervalSeconds:       timing.pollIntervalSeconds,
			CatalogDecisionTTLSeconds: timing.decisionTTLSeconds,
			CatalogExpirySkewSeconds:  timing.expirySkewSeconds,
		},
	}
	for _, info := range infos {
		if strings.EqualFold(strings.TrimSpace(info.Type), "openai-codex-oauth") {
			out.Providers = append(out.Providers, piBootstrapProvider{Kind: "codex", RelayProviderID: info.ID, Models: append([]string(nil), piVoidCodexModels...)})
		}
	}
	return out, nil
}
