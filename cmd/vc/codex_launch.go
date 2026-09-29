package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/makscee/void-code/internal/codexruntime"
	"github.com/makscee/void-code/internal/config"
	"github.com/makscee/void-code/internal/harness/direct"
)

// codexGrantType is the provider type whose grant Codex runs on, the same one
// pi-bootstrap hands Pi as its codex transport.
const codexGrantType = "openai-codex-oauth"

// ensureCodexRuntime returns the pinned Codex, installing it into
// ~/.void-code/runtime/codex/<version> when missing; progress goes to w. A var
// so no test downloads 130–160 MB.
var ensureCodexRuntime = func(w io.Writer) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if home, err = filepath.EvalSymlinks(home); err != nil {
		return "", err
	}
	return codexruntime.Ensure(codexruntime.Options{
		Home:     home,
		BaseURL:  codexruntime.DefaultBaseURL,
		GOOS:     runtime.GOOS,
		GOARCH:   runtime.GOARCH,
		Progress: w,
	})
}

// codexNoDaemon is the only argument Codex gets. Without --no-daemon the 0.158
// TUI starts a `codex app-server --managed-daemon` that outlives the session
// and needs a unix socket under CODEX_HOME, which a long home path breaks
// ("path must be shorter than SUN_LEN").
const codexNoDaemon = "--no-daemon"

// prepareCodexChild readies Codex after admission. The grant is checked
// before the install, so nobody downloads Codex only to be refused; it is a
// live question on every Codex start.
func prepareCodexChild(cfg config.Config, token, notice string) (runtimeChild, error) {
	if cfg.CAOverride != "" {
		return runtimeChild{}, fmt.Errorf("Codex пока работает только с публичным релеем, а задан VC_RELAY_CA=%s. Уберите VC_RELAY_CA или переключитесь на Pi: vc runtime pi", cfg.CAOverride)
	}
	providerID, err := codexProviderID(cfg, token)
	if err != nil {
		return runtimeChild{}, err
	}
	codexPath, err := ensureCodexRuntime(os.Stderr)
	if err != nil {
		return runtimeChild{}, fmt.Errorf("не удалось установить Codex %s: %w\nПопробуйте ещё раз или переключитесь на Pi: vc runtime pi", codexruntime.Version, err)
	}
	cacheDir, err := config.CacheDir()
	if err != nil {
		return runtimeChild{}, err
	}
	codexHome := filepath.Join(cacheDir, "codex")
	if err := codexruntime.WriteConfig(codexHome, fmt.Sprintf("%s://%s", cfg.RelayScheme, cfg.RelayHost)); err != nil {
		return runtimeChild{}, err
	}
	env := buildCodexSpawnEnv(os.Environ(), codexHome, token, providerID, selfDir())
	// Pi shows the wallet notice inside its session; Codex has no such hook,
	// so it is printed before Codex takes the terminal.
	if notice != "" {
		fmt.Fprintln(os.Stderr, "vc: "+notice)
	}
	currentLaunchDiagnostics.record(phaseSpawnHandoff, outcomeComplete, sourceLocal)
	currentLaunchDiagnostics.flush()
	return runtimeChild{exe: codexPath, args: []string{codexNoDaemon}, env: env}, nil
}

// selfDir is the folder of the running vc, or "" when it cannot be told.
func selfDir() string {
	exe, err := os.Executable()
	if err != nil || !filepath.IsAbs(exe) {
		return ""
	}
	return filepath.Dir(exe)
}

// codexProviderID asks auth for the live grants and returns the first
// ChatGPT (openai-codex-oauth) one, in the order auth lists them.
func codexProviderID(cfg config.Config, token string) (string, error) {
	infos, err := fetchProvidersLive(cfg.AuthHost, token, &http.Client{Timeout: authProbeTimeout})
	if err != nil {
		return "", fmt.Errorf("не удалось получить доступы подписки: %w", err)
	}
	for _, info := range infos {
		if strings.EqualFold(strings.TrimSpace(info.Type), codexGrantType) {
			return info.ID, nil
		}
	}
	return "", errors.New("Codex работает только с доступом к ChatGPT. Переключитесь на Pi: vc runtime pi")
}

// buildCodexSpawnEnv is the parent environment without anything that could
// point Codex elsewhere or hand it someone else's credential — OPENAI_API_KEY,
// OPENAI_BASE_URL, every CODEX_* and VC_* variable, and the relay/auth keys
// direct.PlainEnv strips — plus the four values vc sets. PATH stays the
// person's own, with vc's folder put first so `!vc runtime pi` typed in Codex
// reaches this vc: Codex is a native binary and its tools run in the person's
// environment. Names are matched without regard to case, as Windows reads them.
func buildCodexSpawnEnv(parent []string, codexHome, token, providerID, vcDir string) []string {
	base := direct.PlainEnv(parent)
	out := make([]string, 0, len(base)+5)
	for _, e := range base {
		k, _, _ := strings.Cut(e, "=")
		if codexStrippedEnv(k) {
			continue
		}
		out = append(out, e)
	}
	out = withPathFirst(out, vcDir)
	return append(out,
		"CODEX_HOME="+codexHome,
		"VC_AUTH_TOKEN="+token,
		"VC_CODEX_PROVIDER="+providerID,
		"VC_HARNESS=codex",
	)
}

// withPathFirst puts dir at the front of env's PATH (whatever its case, as
// Windows writes Path), or makes it the PATH when there is none.
func withPathFirst(env []string, dir string) []string {
	if dir == "" {
		return env
	}
	for i, e := range env {
		k, v, _ := strings.Cut(e, "=")
		if strings.EqualFold(k, "PATH") {
			if v == "" {
				env[i] = k + "=" + dir
			} else {
				env[i] = k + "=" + dir + string(os.PathListSeparator) + v
			}
			return env
		}
	}
	return append(env, "PATH="+dir)
}

func codexStrippedEnv(key string) bool {
	upper := strings.ToUpper(key)
	return upper == "OPENAI_API_KEY" || upper == "OPENAI_BASE_URL" ||
		strings.HasPrefix(upper, "CODEX_") || strings.HasPrefix(upper, "VC_")
}
