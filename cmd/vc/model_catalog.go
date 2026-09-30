package main

import (
	"net/http"
	"strings"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/config"
)

// piModel is one model vc offers Pi under void-codex.
type piModel struct {
	ID    string
	Label string
}

// piModelCatalog is what vc offers Pi: the live models in picker order
// (default first), the default, and where retired ids moved.
//
// It comes from Keys' catalog via /v1/vc/providers (void-works#81), so a new
// model needs no vc release. builtinPiModelCatalog is only the fallback for a
// server that predates the catalog or cannot be reached.
type piModelCatalog struct {
	Models    []piModel
	Default   string
	RetiredTo map[string]string
}

// builtinPiModelCatalog is the fallback. Keep it in step with the extension's
// CODEX_MODEL_ID and the web-search add-on's fallback list.
func builtinPiModelCatalog() piModelCatalog {
	models := make([]piModel, 0, len(piVoidCodexModels))
	for _, id := range piVoidCodexModels {
		models = append(models, piModel{ID: id})
	}
	return piModelCatalog{
		Models:  models,
		Default: piDefaultModel,
		RetiredTo: map[string]string{
			"gpt-6-sol":    piDefaultModel,
			"gpt-5.6-sol":  piDefaultModel,
			"gpt-5.6-luna": "gpt-6-luna",
		},
	}
}

func isCodexProvider(info auth.ProviderInfo) bool {
	return strings.EqualFold(strings.TrimSpace(info.Type), "openai-codex-oauth")
}

// piModelCatalogFrom reads the catalog of the first codex provider that has
// one. ok is false when none does, or when it offers no live model.
func piModelCatalogFrom(infos []auth.ProviderInfo) (piModelCatalog, bool) {
	for _, info := range infos {
		if !isCodexProvider(info) || len(info.Models) == 0 {
			continue
		}
		return catalogFromModels(info.Models)
	}
	return piModelCatalog{}, false
}

func catalogFromModels(rows []auth.ProviderModel) (piModelCatalog, bool) {
	out := piModelCatalog{RetiredTo: map[string]string{}}
	seen := map[string]bool{}
	for _, row := range rows {
		id := strings.TrimSpace(row.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if to := strings.TrimSpace(row.RetiredTo); to != "" {
			out.RetiredTo[id] = to
			continue
		}
		model := piModel{ID: id, Label: strings.TrimSpace(row.Label)}
		if row.Default && out.Default == "" {
			out.Default = id
			out.Models = append([]piModel{model}, out.Models...)
			continue
		}
		out.Models = append(out.Models, model)
	}
	if len(out.Models) == 0 {
		return piModelCatalog{}, false
	}
	if out.Default == "" {
		out.Default = out.Models[0].ID
	}
	return out, true
}

// live reports whether id is a model the catalog offers now.
func (c piModelCatalog) live(id string) bool {
	for _, m := range c.Models {
		if m.ID == id {
			return true
		}
	}
	return false
}

// replacement follows id's retirement chain to a live model. When the chain
// ends nowhere live, the default takes its place, since the relay refuses a
// retired id either way. ok is false when id is not retired.
func (c piModelCatalog) replacement(id string) (string, bool) {
	to, retired := c.RetiredTo[id]
	if !retired {
		return "", false
	}
	for step := 0; retired && step < 8; step++ {
		if c.live(to) {
			return to, true
		}
		to, retired = c.RetiredTo[to]
	}
	return c.Default, c.Default != ""
}

func (c piModelCatalog) ids() []string {
	out := make([]string, 0, len(c.Models))
	for _, m := range c.Models {
		out = append(out, m.ID)
	}
	return out
}

// currentPiModelCatalog asks the server for the catalog, and falls back to
// the built-in list when there is no token, the server cannot be reached, or
// it has no catalog yet.
var currentPiModelCatalog = func() piModelCatalog {
	token, _, err := auth.Load()
	if err != nil || strings.TrimSpace(token) == "" {
		return builtinPiModelCatalog()
	}
	cfg := config.OSResolve()
	infos, err := fetchProvidersLive(cfg.AuthHost, token, &http.Client{Timeout: authProbeTimeout})
	if err != nil {
		return builtinPiModelCatalog()
	}
	if catalog, ok := piModelCatalogFrom(infos); ok {
		return catalog
	}
	return builtinPiModelCatalog()
}
