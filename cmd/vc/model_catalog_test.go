package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/makscee/void-code/internal/auth"
)

// liveCatalogRows is the shape Keys serves through /v1/vc/providers
// (void-works#81), with a model no vc release knows about.
func liveCatalogRows() []map[string]any {
	return []map[string]any{
		{"id": "gpt-5.6-sol", "label": "GPT-5.6 Sol", "default": false, "retired_to": "gpt-6.1-sol"},
		{"id": "gpt-5.6-terra", "label": "GPT-5.6 Terra", "default": false, "retired_to": nil},
		{"id": "gpt-6-sol", "label": "GPT-6 Sol", "default": false, "retired_to": "gpt-6.1-sol"},
		{"id": "gpt-6.1-sol", "label": "GPT-6.1 Sol", "default": false, "retired_to": "gpt-7-test"},
		{"id": "gpt-7-test", "label": "GPT-7 Test", "default": true, "retired_to": nil},
		{"id": "gpt-6-luna", "label": "GPT-6 Luna", "default": false, "retired_to": nil},
		// Retired to an id the catalog no longer offers: the default takes its place.
		{"id": "gpt-5.5-gone", "label": "GPT-5.5", "default": false, "retired_to": "gpt-5.5-missing"},
	}
}

func serveCatalog(t *testing.T, rows []map[string]any) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"providers": []map[string]any{
			{"id": "chatgpt-granted", "name": "ChatGPT", "type": "openai-codex-oauth", "models": rows},
		}})
	}))
	t.Cleanup(server.Close)
	t.Setenv("VC_AUTH_HOST", server.URL)
	t.Setenv("VC_RELAY_HOST", "https://relay.test:9443")
	if err := auth.Save("protected-token"); err != nil {
		t.Fatal(err)
	}
}

func TestPiBootstrapBuildsPickerFromTheCatalog(t *testing.T) {
	piSettingsSandbox(t)
	serveCatalog(t, liveCatalogRows())

	got, err := currentPiBootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Providers) != 1 {
		t.Fatalf("providers = %#v", got.Providers)
	}
	p := got.Providers[0]
	if want := []string{"gpt-7-test", "gpt-5.6-terra", "gpt-6-luna"}; !reflect.DeepEqual(p.Models, want) {
		t.Errorf("models = %q, want live catalog models, default first: %q", p.Models, want)
	}
	wantInfo := []piBootstrapModel{
		{ID: "gpt-7-test", Label: "GPT-7 Test", Default: true},
		{ID: "gpt-5.6-terra", Label: "GPT-5.6 Terra"},
		{ID: "gpt-6-luna", Label: "GPT-6 Luna"},
	}
	if !reflect.DeepEqual(p.ModelInfo, wantInfo) {
		t.Errorf("modelInfo = %#v, want %#v", p.ModelInfo, wantInfo)
	}
}

// A server that predates the catalog still gets the built-in picker.
func TestPiBootstrapFallsBackWithoutCatalog(t *testing.T) {
	piSettingsSandbox(t)
	serveCatalog(t, nil)

	got, err := currentPiBootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Providers) != 1 || !reflect.DeepEqual(got.Providers[0].Models, piVoidCodexModels) {
		t.Fatalf("providers = %#v, want the built-in %q", got.Providers, piVoidCodexModels)
	}
}

func TestCurrentPiModelCatalogReadsServerAndFallsBack(t *testing.T) {
	piSettingsSandbox(t)
	if got := currentPiModelCatalog(); !reflect.DeepEqual(got, builtinPiModelCatalog()) {
		t.Errorf("no token: catalog = %#v, want built-in", got)
	}

	serveCatalog(t, liveCatalogRows())
	got := currentPiModelCatalog()
	if got.Default != "gpt-7-test" || got.RetiredTo["gpt-6-sol"] != "gpt-6.1-sol" {
		t.Errorf("catalog = %#v, want the server's", got)
	}

	t.Setenv("VC_AUTH_HOST", "http://127.0.0.1:1")
	if got := currentPiModelCatalog(); !reflect.DeepEqual(got, builtinPiModelCatalog()) {
		t.Errorf("unreachable: catalog = %#v, want built-in", got)
	}
}

// Maks (void-works#81): a user whose saved model is retired moves to its
// replacement, following the chain to a live model.
func TestEnsurePiDefaultModelMovesRetiredModelToCatalogReplacement(t *testing.T) {
	rows := make([]auth.ProviderModel, 0)
	for _, r := range liveCatalogRows() {
		to, _ := r["retired_to"].(string)
		rows = append(rows, auth.ProviderModel{ID: r["id"].(string), Label: r["label"].(string), Default: r["default"].(bool), RetiredTo: to})
	}
	catalog, ok := catalogFromModels(rows)
	if !ok {
		t.Fatal("catalog not built")
	}
	for _, tc := range []struct{ body, wantModel string }{
		{`{"defaultProvider":"void-codex","defaultModel":"gpt-5.6-sol","theme":"nord"}`, "gpt-7-test"},
		{`{"defaultProvider":"void-codex","defaultModel":"gpt-6.1-sol","theme":"nord"}`, "gpt-7-test"},
		{`{"defaultProvider":"void-codex","defaultModel":"gpt-6-luna","theme":"nord"}`, "gpt-6-luna"},
		{`{"defaultProvider":"void-codex","defaultModel":"some-own-model","theme":"nord"}`, "some-own-model"},
		{`{"defaultProvider":"void-codex","defaultModel":"gpt-5.5-gone","theme":"nord"}`, "gpt-7-test"},
		{`{"defaultProvider":"void-codex","theme":"nord"}`, "gpt-7-test"},
		{`{"defaultProvider":"void-deepseek","defaultModel":"deepseek-chat","theme":"nord"}`, "gpt-7-test"},
	} {
		dir := piSettingsSandbox(t)
		path := writePiSettings(t, dir, tc.body, 0600)
		if err := ensurePiDefaultModelFrom(catalog); err != nil {
			t.Fatal(err)
		}
		after := readPiSettings(t, path)
		if after["defaultModel"] != tc.wantModel || after["defaultProvider"] != "void-codex" || after["theme"] != "nord" {
			t.Errorf("%s → %#v, want defaultModel %q", tc.body, after, tc.wantModel)
		}
	}

	// A foreign provider's model is never touched, even with a retired id.
	dir := piSettingsSandbox(t)
	path := writePiSettings(t, dir, `{"defaultProvider":"openai","defaultModel":"gpt-6-sol"}`, 0600)
	if err := ensurePiDefaultModelFrom(catalog); err != nil {
		t.Fatal(err)
	}
	if after := readPiSettings(t, path); after["defaultModel"] != "gpt-6-sol" {
		t.Errorf("foreign provider rewritten: %#v", after)
	}
}
