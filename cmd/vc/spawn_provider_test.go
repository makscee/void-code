package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/makscee/void-code/internal/harnesschoice"
	"github.com/makscee/void-code/internal/provider"
)

func findEnv(env []string, key string) (string, bool) {
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		if k == key {
			return v, true
		}
	}
	return "", false
}

func TestBuildSpawnEnv_Relay(t *testing.T) {
	env, err := buildSpawnEnv(provider.Provider{Kind: provider.Relay},
		[]string{"PATH=/usr/bin"}, "https", "relay.makscee.ru:443", "pool-tok", "/etc/relay-ca.pem")
	if err != nil {
		t.Fatalf("relay: %v", err)
	}
	if v, ok := findEnv(env, "HTTPS_PROXY"); !ok || v != "https://relay.makscee.ru:443" {
		t.Errorf("relay HTTPS_PROXY = %q ok=%v", v, ok)
	}
	if v, ok := findEnv(env, "NODE_EXTRA_CA_CERTS"); !ok || v != "/etc/relay-ca.pem" {
		t.Errorf("relay NODE_EXTRA_CA_CERTS = %q ok=%v", v, ok)
	}
	if v, ok := findEnv(env, "ANTHROPIC_BASE_URL"); !ok || v != "" {
		t.Errorf("relay ANTHROPIC_BASE_URL = %q ok=%v (want empty)", v, ok)
	}
	if v, _ := findEnv(env, "ANTHROPIC_AUTH_TOKEN"); v != "pool-tok" {
		t.Errorf("relay token = %q", v)
	}
	if _, ok := findEnv(env, "CLAUDE_CODE_OAUTH_TOKEN"); ok {
		t.Error("relay must not emit CLAUDE_CODE_OAUTH_TOKEN")
	}
}

func TestBuildSpawnEnvRelayProviderInjectsHeader(t *testing.T) {
	p := provider.Provider{Kind: provider.RelayProvider, ID: "plat-2"}
	env, err := buildSpawnEnv(p, []string{}, "https", "relay.example:8448", "pooltok", "/etc/relay-ca.pem")
	if err != nil {
		t.Fatalf("buildSpawnEnv: %v", err)
	}
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "ANTHROPIC_CUSTOM_HEADERS=x-void-provider: plat-2") {
		t.Fatalf("missing custom header; env=%v", env)
	}
	// still the relay path: proxy + pool token present.
	if !strings.Contains(joined, "HTTPS_PROXY=https://relay.example:8448") ||
		!strings.Contains(joined, "ANTHROPIC_AUTH_TOKEN=pooltok") {
		t.Fatalf("relay proxy vars missing; env=%v", env)
	}
}

func TestBuildSpawnEnvBareRelayHasNoHeader(t *testing.T) {
	p := provider.Provider{Kind: provider.Relay}
	env, _ := buildSpawnEnv(p, []string{}, "https", "relay.example:8448", "pooltok", "/etc/relay-ca.pem")
	if strings.Contains(strings.Join(env, "\n"), "ANTHROPIC_CUSTOM_HEADERS") {
		t.Fatalf("bare Relay must not inject x-void-provider; env=%v", env)
	}
}

func TestWrappedBinaryForHarness(t *testing.T) {
	if got := wrappedBinaryFor(harnesschoice.Choice{Kind: harnesschoice.Claude}); got != "claude" {
		t.Fatalf("Claude wrapped binary = %q, want claude", got)
	}
	if got := wrappedBinaryFor(harnesschoice.Choice{Kind: harnesschoice.Codex}); got != "codex" {
		t.Fatalf("Codex wrapped binary = %q, want codex", got)
	}
	if got := wrappedBinaryFor(harnesschoice.Choice{Kind: harnesschoice.Pi}); got != "pi" {
		t.Fatalf("Pi wrapped binary = %q, want pi", got)
	}
}

func TestEnsureSelectedHarnessInstalled_PiMissing(t *testing.T) {
	oldPiInstalled := piIsInstalled
	oldClaudeInstalled := claudeIsInstalled
	oldCodexInstalled := codexIsInstalled
	t.Cleanup(func() {
		piIsInstalled = oldPiInstalled
		claudeIsInstalled = oldClaudeInstalled
		codexIsInstalled = oldCodexInstalled
	})
	piIsInstalled = func() bool { return false }
	claudeIsInstalled = func() bool { return true }
	codexIsInstalled = func() bool { return true }

	err := ensureSelectedHarnessInstalled(harnesschoice.Choice{Kind: harnesschoice.Pi})
	if err == nil || !strings.Contains(err.Error(), "pi CLI not found") {
		t.Fatalf("Pi missing err = %v, want clear missing-pi message", err)
	}
}

func TestEnsureSelectedHarnessInstalled_CodexMissing(t *testing.T) {
	oldPiInstalled := piIsInstalled
	oldClaudeInstalled := claudeIsInstalled
	oldCodexInstalled := codexIsInstalled
	t.Cleanup(func() {
		piIsInstalled = oldPiInstalled
		claudeIsInstalled = oldClaudeInstalled
		codexIsInstalled = oldCodexInstalled
	})
	piIsInstalled = func() bool { return true }
	claudeIsInstalled = func() bool { return true }
	codexIsInstalled = func() bool { return false }

	err := ensureSelectedHarnessInstalled(harnesschoice.Choice{Kind: harnesschoice.Codex})
	if err == nil || !strings.Contains(err.Error(), "codex CLI not found") {
		t.Fatalf("Codex missing err = %v, want clear missing-codex message", err)
	}
}

func TestBuildCodexArgsInjectsConfigBeforeUserArgs(t *testing.T) {
	got := buildCodexArgs([]string{"--ask-for-approval", "never"}, "https", "relay.example:443")
	joined := strings.Join(got, "\x00")
	for _, want := range []string{
		"model_provider=void",
		"model_providers.void.base_url=https://relay.example:443/codex",
		"model_providers.void.wire_api=responses",
		"model_providers.void.env_key=VC_AUTH_TOKEN",
		"model_providers.void.env_http_headers.x-void-provider=VC_RELAY_PROVIDER_ID",
		"model=gpt-5.6-terra",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("Codex args missing %q: %#v", want, got)
		}
	}
	if got[len(got)-2] != "--ask-for-approval" || got[len(got)-1] != "never" {
		t.Fatalf("user args not preserved at end: %#v", got)
	}
}

func TestBuildCodexArgsPreservesUserModelOverride(t *testing.T) {
	got := buildCodexArgs([]string{"--model", "user-choice", "-p", "hello"}, "https", "relay.example:443")
	wantSuffix := []string{"--model", "user-choice", "-p", "hello"}
	if strings.Join(got[len(got)-len(wantSuffix):], "\x00") != strings.Join(wantSuffix, "\x00") {
		t.Fatalf("user model override not preserved: %#v", got)
	}
}

func TestBuildCodexSpawnEnvUsesVCSeamAndStripsSecrets(t *testing.T) {
	env := buildCodexSpawnEnv(provider.Provider{Kind: provider.RelayProvider, ID: "chatgpt-sub"},
		[]string{"PATH=/usr/bin", "OPENAI_API_KEY=sk-old", "VC_AUTH_TOKEN=old", "CHATGPT_ACCOUNT_ID=acct"},
		"https", "relay.example:443", "pooltok", "/tmp/ca.pem")
	joined := strings.Join(env, "\n")
	for _, leak := range []string{"OPENAI_API_KEY", "CHATGPT_ACCOUNT_ID=acct", "VC_AUTH_TOKEN=old"} {
		if strings.Contains(joined, leak) {
			t.Fatalf("Codex env leaked %q: %v", leak, env)
		}
	}
	for _, want := range []string{"VC_HARNESS=codex", "VC_PROVIDER=relay", "VC_RELAY_PROVIDER_ID=chatgpt-sub", "VC_RELAY_URL=https://relay.example:443", "VC_RELAY_CA=/tmp/ca.pem", "VC_AUTH_TOKEN=pooltok"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("Codex env missing %q: %v", want, env)
		}
	}
}

func TestBuildPiSpawnEnvRelayProviderUsesVCSeamAndStripsClaude(t *testing.T) {
	env := buildPiSpawnEnv(provider.Provider{Kind: provider.RelayProvider, ID: "deepseek"},
		[]string{"PATH=/usr/bin", "ANTHROPIC_AUTH_TOKEN=old", "HTTPS_PROXY=old", "VC_AUTH_TOKEN=old", "OPENAI_API_KEY=sk-old", "CHATGPT_ACCOUNT_ID=acct", "ANTHROPIC_CUSTOM_HEADERS=x-void-provider: stale"},
		"https", "relay.example:443", "pooltok", "/tmp/ca.pem")
	joined := strings.Join(env, "\n")
	for _, leak := range []string{"ANTHROPIC_AUTH_TOKEN", "HTTPS_PROXY=old", "OPENAI_API_KEY", "CHATGPT_ACCOUNT_ID", "ANTHROPIC_CUSTOM_HEADERS"} {
		if strings.Contains(joined, leak) {
			t.Fatalf("Pi env leaked client secret %q: %v", leak, env)
		}
	}
	for _, want := range []string{
		"VC_HARNESS=pi",
		"VC_PROVIDER=relay",
		"VC_RELAY_PROVIDER_ID=deepseek",
		"VC_RELAY_URL=https://relay.example:443",
		"VC_RELAY_CA=/tmp/ca.pem",
		"VC_AUTH_TOKEN=pooltok",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("Pi env missing %q: %v", want, env)
		}
	}
}

func TestBuildPiSpawnEnvBareRelayUsesDeepSeekSentinel(t *testing.T) {
	env := buildPiSpawnEnv(provider.Provider{Kind: provider.Relay},
		[]string{"PATH=/usr/bin", "ANTHROPIC_AUTH_TOKEN=old", "VC_RELAY_PROVIDER_ID=old", "VC_AUTH_TOKEN=old"},
		"https", "relay.example:443", "pooltok", "/tmp/ca.pem")
	joined := strings.Join(env, "\n")
	for _, leak := range []string{"ANTHROPIC_AUTH_TOKEN", "VC_AUTH_TOKEN=old", "VC_RELAY_PROVIDER_ID=old"} {
		if strings.Contains(joined, leak) {
			t.Fatalf("Pi bare relay env leaked %q: %v", leak, env)
		}
	}
	for _, want := range []string{
		"VC_HARNESS=pi",
		"VC_PROVIDER=relay",
		"VC_RELAY_PROVIDER_ID=deepseek",
		"VC_RELAY_URL=https://relay.example:443",
		"VC_RELAY_CA=/tmp/ca.pem",
		"VC_AUTH_TOKEN=pooltok",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("Pi bare relay env missing %q: %v", want, env)
		}
	}
}

func TestBuildPiVoidCodexArgsInjectsExtensionProviderModel(t *testing.T) {
	got := buildPiVoidCodexArgs([]string{"-p", "hello"}, "/tmp/vc-pi/index.ts")
	want := []string{"-e", "/tmp/vc-pi/index.ts", "--provider", "void-codex", "--model", "gpt-5.6-terra", "-p", "hello"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestBuildPiVoidCodexArgsKeepsUserProviderModel(t *testing.T) {
	got := buildPiVoidCodexArgs([]string{"--provider=other", "--model", "other-model", "-p", "hello"}, "/tmp/vc-pi/index.ts")
	joined := strings.Join(got, " ")
	if strings.Count(joined, "--provider") != 1 || strings.Count(joined, "--model") != 1 {
		t.Fatalf("provider/model flags duplicated: %#v", got)
	}
	if !strings.HasPrefix(joined, "-e /tmp/vc-pi/index.ts --provider=other --model other-model") {
		t.Fatalf("user provider/model order not preserved after extension: %#v", got)
	}
}

func TestBuildPiVoidDeepSeekArgsInjectsExtensionProviderModel(t *testing.T) {
	got := buildPiVoidDeepSeekArgs([]string{"-p", "hello"}, "/tmp/vc-pi/index.ts")
	want := []string{"-e", "/tmp/vc-pi/index.ts", "--provider", "void-deepseek", "--model", "deepseek/deepseek-v4-pro", "-p", "hello"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestBuildPiVoidDeepSeekArgsKeepsUserProviderModel(t *testing.T) {
	got := buildPiVoidDeepSeekArgs([]string{"--provider=other", "--model", "other-model", "-p", "hello"}, "/tmp/vc-pi/index.ts")
	joined := strings.Join(got, " ")
	if strings.Count(joined, "--provider") != 1 || strings.Count(joined, "--model") != 1 {
		t.Fatalf("provider/model flags duplicated: %#v", got)
	}
	if !strings.HasPrefix(joined, "-e /tmp/vc-pi/index.ts --provider=other --model other-model") {
		t.Fatalf("user provider/model order not preserved after extension: %#v", got)
	}
}

func TestEnsurePiVoidCodexExtensionWritesOwnedProvider(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	path, err := ensurePiVoidCodexExtension()
	if err != nil {
		t.Fatalf("ensurePiVoidCodexExtension: %v", err)
	}
	if filepath.Base(path) != "index.ts" {
		t.Fatalf("extension path = %q, want index.ts", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read extension: %v", err)
	}
	src := string(data)
	for _, want := range []string{"pi.registerProvider(CODEX_PROVIDER_ID", "void-codex", "Void ChatGPT relay", "GPT-5.6 Sol via Void relay", "GPT-5.6 Terra via Void relay", "gpt-5.6-sol", "gpt-5.6-terra", "contextWindow: 1050000", "maxTokens: 128000", "/codex/responses", "authorization\": \"Bearer ", "pi.registerProvider(DEEPSEEK_PROVIDER_ID", "void-deepseek", "anthropic-messages", "deepseek/deepseek-v4-pro", "authHeader: true", "x-void-provider"} {
		if !strings.Contains(src, want) {
			t.Fatalf("extension missing %q", want)
		}
	}
	for _, forbidden := range []string{"Void Codex relay", "gpt-5.5", "gpt-5.4", "gpt-5.3-codex-spark", "chatgpt-account-id", "OPENAI_API_KEY", "CHATGPT_ACCOUNT_ID"} {
		if strings.Contains(src, forbidden) {
			t.Fatalf("extension contains forbidden client secret/account material %q", forbidden)
		}
	}
}

// TestPiRelayListModelsDoesNotInheritStandaloneCodexAuth covers the actual Pi
// process boundary: --provider selects a model but does not hide providers Pi
// discovers from its global auth registry. vc must use its own Pi config root.
func TestPiRelayListModelsDoesNotInheritStandaloneCodexAuth(t *testing.T) {
	piBin, err := exec.LookPath("pi")
	if err != nil {
		t.Skip("pi CLI not installed")
	}

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	standaloneDir := filepath.Join(tmp, ".pi", "agent")
	if err := os.MkdirAll(standaloneDir, 0700); err != nil {
		t.Fatal(err)
	}
	// A structurally valid native Codex OAuth record is enough for Pi to expose
	// its built-in openai-codex registry; no live credential is used here.
	if err := os.WriteFile(filepath.Join(standaloneDir, "auth.json"), []byte(`{"openai-codex":{"type":"oauth","access":"test","refresh":"test","expires":4102444800000,"accountId":"test"}}`), 0600); err != nil {
		t.Fatal(err)
	}

	extPath, err := ensurePiVoidCodexExtension()
	if err != nil {
		t.Fatalf("ensure extension: %v", err)
	}
	env := buildPiSpawnEnv(provider.Provider{Kind: provider.RelayProvider, ID: "chatgpt-sub"}, os.Environ(), "https", "relay.example:443", "smoke-token", "/tmp/ca.pem")
	env, err = isolatePiRelayConfig(env, extPath)
	if err != nil {
		t.Fatalf("isolate Pi relay config: %v", err)
	}
	args := buildPiVoidCodexArgs([]string{"--no-extensions", "--list-models"}, extPath)
	cmd := exec.Command(piBin, args...)
	cmd.Env = append(env, "PI_OFFLINE=1", "PI_TELEMETRY=0")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("Pi list-models failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	list := stdout.String()
	for _, want := range []string{
		"void-codex     gpt-5.6-sol",
		"void-codex     gpt-5.6-terra",
		"void-deepseek  deepseek/deepseek-v4-pro",
	} {
		if !strings.Contains(list, want) {
			t.Fatalf("Pi list missing managed row %q:\n%s", want, list)
		}
	}
	for _, forbidden := range []string{"openai-codex", "gpt-5.3", "gpt-5.4", "gpt-5.5", "gpt-5.6-luna"} {
		if strings.Contains(list, forbidden) {
			t.Fatalf("Pi list inherited forbidden native model/provider %q:\n%s", forbidden, list)
		}
	}
}

func TestPiVoidCodexPickerOffersOnlyGPT56Models(t *testing.T) {
	for _, modelID := range []string{"gpt-5.6-sol", "gpt-5.6-terra"} {
		if !strings.Contains(piVoidCodexExtensionSource, modelID) {
			t.Fatalf("picker missing supported model %q", modelID)
		}
	}
	if !strings.Contains(piVoidCodexExtensionSource, `const LUNA_PICKER_ENABLED = false;`) {
		t.Fatal("Luna picker must be disabled by default")
	}
	for _, prerequisite := range []string{
		"LUNA_LIVE_OAUTH_SMOKE_PREREQUISITE",
		`VC_RELAY_URL="$VC_RELAY_URL" VC_AUTH_TOKEN="$VC_AUTH_TOKEN" VC_RELAY_PROVIDER_ID="$VC_RELAY_PROVIDER_ID"`,
		`--provider void-codex --model gpt-5.6-luna`,
		`-p 'Reply exactly: luna smoke ok'`,
		"Require HTTP success and the exact response from the live OAuth relay.",
	} {
		if !strings.Contains(piVoidCodexExtensionSource, prerequisite) {
			t.Fatalf("Luna default-off gate requires live OAuth smoke prerequisite %q", prerequisite)
		}
	}
	lunaGate := `...(LUNA_PICKER_ENABLED ? [codexModel("gpt-5.6-luna", "GPT-5.6 Luna via Void relay")] : []),`
	if !strings.Contains(piVoidCodexExtensionSource, lunaGate) {
		t.Fatal("Luna picker must remain behind the explicit dormant gate")
	}
	for _, unsupported := range []string{"gpt-5.6\"", "gpt-5.5", "gpt-5.4", "gpt-5.3-codex-spark"} {
		if strings.Contains(piVoidCodexExtensionSource, unsupported) {
			t.Fatalf("picker exposes unsupported model %q", unsupported)
		}
	}
	if piVoidCodexModel != "gpt-5.6-terra" {
		t.Fatalf("Pi default = %q, want gpt-5.6-terra", piVoidCodexModel)
	}
	if !strings.Contains(piVoidCodexExtensionSource, `const CODEX_MODEL_ID = "gpt-5.6-terra";`) {
		t.Fatal("Pi extension default must be gpt-5.6-terra")
	}
	if got := strings.Join(buildCodexArgs(nil, "https", "relay.example:443"), "\x00"); !strings.Contains(got, "model=gpt-5.6-terra") {
		t.Fatalf("Codex default missing gpt-5.6-terra: %q", got)
	}
}

func TestBuildSpawnEnv_Plain(t *testing.T) {
	env, err := buildSpawnEnv(provider.Provider{Kind: provider.Plain},
		[]string{"PATH=/usr/bin", "HTTPS_PROXY=x"}, "https", "h", "tok", "/etc/relay-ca.pem")
	if err != nil {
		t.Fatalf("plain: %v", err)
	}
	if _, ok := findEnv(env, "HTTPS_PROXY"); ok {
		t.Error("plain must strip HTTPS_PROXY")
	}
	if _, ok := findEnv(env, "ANTHROPIC_AUTH_TOKEN"); ok {
		t.Error("plain must inject no token")
	}
}
